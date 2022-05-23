package distributor

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"strconv"
	"sync"
	"testing"
	"time"

	"global-resource-service/resource-management/pkg/common-lib/types"
	"global-resource-service/resource-management/pkg/common-lib/types/event"
	"global-resource-service/resource-management/pkg/common-lib/types/location"
)

func TestSingleRPMutipleClients_Workflow(t *testing.T) {
	testCases := []struct {
		name           string
		nodeNum        int
		clientNum      int
		hostPerClient  int
		updateEventNum int
	}{
		{
			name:           "Test 10K nodes with 5 clients each has 500 hosts, each got 1K update events",
			nodeNum:        10000,
			clientNum:      5,
			hostPerClient:  500,
			updateEventNum: 1000,
		},
		{
			name:           "Test 10K nodes with 5 clients each has 500 , each got 10K update events",
			nodeNum:        10000,
			clientNum:      5,
			hostPerClient:  500,
			updateEventNum: 10000,
		},
		{
			name:           "Test 10K nodes with 5 clients each has 500 , each got 100K update events",
			nodeNum:        10000,
			clientNum:      5,
			hostPerClient:  500,
			updateEventNum: 100000,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			distributor := setUp()
			defer tearDown()

			// initialize node store with tt.nodeNum nodes
			eventsAdd := generateAddNodeEvent(tt.nodeNum, defaultLocBeijing_RP1)

			start := time.Now()
			result, rvMap := distributor.ProcessEvents(eventsAdd)
			duration := time.Since(start)

			assert.True(t, result)
			assert.NotNil(t, rvMap)
			assert.Equal(t, tt.nodeNum, distributor.defaultNodeStore.GetTotalHostNum())

			// register clients
			clientIds := make([]string, tt.clientNum)
			for i := 0; i < tt.clientNum; i++ {
				start = time.Now()
				clientId, result, err := distributor.RegisterClient(tt.hostPerClient)
				duration += time.Since(start)

				assert.True(t, result, "Expecting register client successfully")
				assert.NotNil(t, clientId, "Expecting not nil client id")
				assert.False(t, clientId == "", "Expecting non empty client id")
				assert.Nil(t, err, "Expecting nil error")
				clientIds[i] = clientId
			}

			// client list nodes
			latestRVsByClient := make([]types.ResourceVersionMap, tt.clientNum)
			nodesByClient := make([][]*types.Node, tt.clientNum)
			for i := 0; i < tt.clientNum; i++ {
				clientId := clientIds[i]

				start = time.Now()
				nodes, latestRVs, err := distributor.ListNodesForClient(clientId)
				duration += time.Since(start)

				assert.Nil(t, err)
				assert.NotNil(t, latestRVs)
				assert.True(t, len(nodes) >= tt.hostPerClient)
				// fmt.Printf("Client %d %s latest rvs: %v.Total hosts: %d\n", i, clientId, latestRVs, len(nodes))
				latestRVsByClient[i] = latestRVs
				nodesByClient[i] = nodes

				// check each node event
				nodeIds := make(map[string]bool)
				for _, node := range nodes {
					assert.NotNil(t, node.GetLocation())
					assert.True(t, latestRVs[*node.GetLocation()] >= node.GetResourceVersion())
					if _, isOK := nodeIds[node.GetId()]; isOK {
						assert.Fail(t, "List nodes cannot have more than one copy of a node")
					} else {
						nodeIds[node.GetId()] = true
					}
				}
				assert.Equal(t, len(nodes), len(nodeIds))
			}

			// clients watch nodes
			stopCh := make(chan struct{})
			allWaitGroup := new(sync.WaitGroup)
			start = time.Now()
			for i := 0; i < tt.clientNum; i++ {
				watchCh := make(chan *event.NodeEvent)
				err := distributor.Watch(clientIds[i], latestRVsByClient[i], watchCh, stopCh)
				if err != nil {
					assert.Fail(t, "Encountered error while building watch connection.", "Encountered error while building watch connection. Error %v", err)
					return
				}
				allWaitGroup.Add(1)

				go func(expectedEventCount int, watchCh chan *event.NodeEvent, wg *sync.WaitGroup) {
					eventCount := 0

					for e := range watchCh {
						assert.Equal(t, event.Modified, e.Type)
						eventCount++

						if eventCount >= expectedEventCount {
							wg.Done()
							return
						}
					}
				}(tt.updateEventNum, watchCh, allWaitGroup)
			}

			// update nodes
			for i := 0; i < tt.clientNum; i++ {
				go func(expectedEventCount int, nodes []*types.Node, clientId string) {
					for j := 0; j < expectedEventCount/len(nodes)+2; j++ {
						updateNodeEvents := make([]*event.NodeEvent, len(nodes))
						for k := 0; k < len(nodes); k++ {
							rvToGenerate += 1
							updateNodeEvents[k] = event.NewNodeEvent(
								types.NewNode(nodes[k].GetId(), strconv.Itoa(rvToGenerate), "", nodes[k].GetLocation()),
								event.Modified)
						}
						result, rvMap := distributor.ProcessEvents(updateNodeEvents)
						assert.True(t, result)
						assert.NotNil(t, rvMap)
						//fmt.Printf("Successfully processed %d update node events. RV map returned: %v. ClientId %s\n", len(nodes), rvMap, clientId)
					}
				}(tt.updateEventNum, nodesByClient[i], clientIds[i])
			}

			// wait for watch done
			allWaitGroup.Wait()
			duration += time.Since(start)
			fmt.Printf("Test %s succeed! Total duration %v\n", tt.name, duration)
		})
	}
}

func TestMultipleRPsMutipleClients_Workflow(t *testing.T) {
	testCases := []struct {
		name           string
		regionNum      int
		rpNum          int
		hostPerRP      int
		clientNum      int
		hostPerClient  int
		updateEventNum int
	}{
		{
			name:           "Test 1 region, 10 RP, 20K hosts per RP, 200K hosts with 10 clients, each got 1K hosts, 10K update events",
			regionNum:      1,
			rpNum:          10,
			hostPerRP:      20000,
			clientNum:      10,
			hostPerClient:  1000,
			updateEventNum: 10000,
		},
		{
			name:           "Test 5 region, each has 20 RPs, 20K hosts per RP, 2M nodes with 100 clients, each got 10K hosts, 10K update events",
			regionNum:      5,
			rpNum:          20,
			hostPerRP:      20000,
			clientNum:      100,
			hostPerClient:  10000,
			updateEventNum: 10000,
		},
		{
			name:           "Test 6 region, each has 20 RPs, 40K hosts per RP, 4.8M nodes with 200 clients, each got 20K hosts, 20K update events",
			regionNum:      6,
			rpNum:          20,
			hostPerRP:      40000,
			clientNum:      200,
			hostPerClient:  20000,
			updateEventNum: 20000,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			virutalStoreNumPerResourcePartition = tt.hostPerRP / 50
			distributor := setUp()
			defer tearDown()

			// create nodes
			eventsAdd := make([][][]*event.NodeEvent, tt.regionNum)
			for i := 0; i < tt.regionNum; i++ {
				regionName := location.Regions[i]
				eventsAdd[i] = make([][]*event.NodeEvent, tt.rpNum)
				for j := 0; j < tt.rpNum; j++ {
					rpName := location.ResourcePartitions[j]
					loc := location.NewLocation(regionName, rpName)

					eventsAdd[i][j] = generateAddNodeEvent(tt.hostPerRP, loc)
				}
			}

			wg := &sync.WaitGroup{}
			wg.Add(tt.regionNum * tt.rpNum)

			start := time.Now()
			for i := 0; i < tt.regionNum; i++ {
				for j := 0; j < tt.rpNum; j++ {
					go func(done *sync.WaitGroup, events []*event.NodeEvent) {
						result, rvMap := distributor.ProcessEvents(events)
						done.Done()
						assert.True(t, result)
						assert.NotNil(t, rvMap)
					}(wg, eventsAdd[i][j])
				}
			}
			wg.Wait()
			duration := time.Since(start)

			// register clients
			clientIds := make([]string, tt.clientNum)
			wg.Add(tt.clientNum)

			start = time.Now()
			for i := 0; i < tt.clientNum; i++ {
				go func(done *sync.WaitGroup, hostPerClient int, clientIds []string, i int) {
					clientId, result, err := distributor.RegisterClient(hostPerClient)
					clientIds[i] = clientId
					done.Done()

					assert.True(t, result, "Expecting register client successfully")
					assert.NotNil(t, clientId, "Expecting not nil client id")
					assert.False(t, clientId == "", "Expecting non empty client id")
					assert.Nil(t, err, "Expecting nil error")
				}(wg, tt.hostPerClient, clientIds, i)
			}
			wg.Wait()
			duration += time.Since(start)

			// client list nodes
			latestRVsByClient := make([]types.ResourceVersionMap, tt.clientNum)
			nodesByClient := make([][]*types.Node, tt.clientNum)
			wg.Add(tt.clientNum)

			start = time.Now()
			for i := 0; i < tt.clientNum; i++ {
				go func(done *sync.WaitGroup, clientId string, i int) {
					nodes, latestRVs, err := distributor.ListNodesForClient(clientId)
					done.Done()

					assert.Nil(t, err)
					assert.NotNil(t, latestRVs)
					assert.True(t, len(nodes) >= tt.hostPerClient)
					// fmt.Printf("Client %d %s latest rvs: %v.Total hosts: %d\n", i, clientId, latestRVs, len(nodes))
					latestRVsByClient[i] = latestRVs
					nodesByClient[i] = nodes

					// check each node event
					nodeIds := make(map[string]bool)
					for _, node := range nodes {
						assert.NotNil(t, node.GetLocation())
						assert.True(t, latestRVs[*node.GetLocation()] >= node.GetResourceVersion())
						if _, isOK := nodeIds[node.GetId()]; isOK {
							assert.Fail(t, "List nodes cannot have more than one copy of a node")
						} else {
							nodeIds[node.GetId()] = true
						}
					}
					assert.Equal(t, len(nodes), len(nodeIds))
				}(wg, clientIds[i], i)
			}
			wg.Wait()
			duration += time.Since(start)

			// clients watch nodes
			allWaitGroup := new(sync.WaitGroup)
			start = time.Now()
			for i := 0; i < tt.clientNum; i++ {
				watchCh := make(chan *event.NodeEvent)
				stopCh := make(chan struct{})
				err := distributor.Watch(clientIds[i], latestRVsByClient[i], watchCh, stopCh)
				if err != nil {
					assert.Fail(t, "Encountered error while building watch connection.", "Encountered error while building watch connection. Error %v", err)
					return
				}
				allWaitGroup.Add(1)

				go func(expectedEventCount int, watchCh chan *event.NodeEvent, wg *sync.WaitGroup) {
					eventCount := 0

					for e := range watchCh {
						assert.Equal(t, event.Modified, e.Type)
						eventCount++

						if eventCount >= expectedEventCount {
							wg.Done()
							fmt.Printf("Successfully watched %d update node events.**************************\n", expectedEventCount)
							return
						}
					}
				}(tt.updateEventNum, watchCh, allWaitGroup)
			}

			fmt.Printf("Starting to watch update events ##################\n")

			// update nodes
			for i := 0; i < tt.clientNum; i++ {
				go func(expectedEventCount int, nodes []*types.Node, clientId string) {
					eventCount := 0

					for j := 0; j < expectedEventCount/len(nodes)+2; j++ {
						updateNodeEvents := make([]*event.NodeEvent, len(nodes))
						for k := 0; k < len(nodes); k++ {
							rvToGenerate += 1
							updateNodeEvents[k] = event.NewNodeEvent(
								types.NewNode(nodes[k].GetId(), strconv.Itoa(rvToGenerate), "", nodes[k].GetLocation()),
								event.Modified)

							eventCount++
							if eventCount >= expectedEventCount {
								break
							}
						}
						result, rvMap := distributor.ProcessEvents(updateNodeEvents)
						assert.True(t, result)
						assert.NotNil(t, rvMap)
						//fmt.Printf("Successfully processed %d update node events. RV map returned: %v. ClientId %s\n", len(nodes), rvMap, clientId)
					}
				}(tt.updateEventNum, nodesByClient[i], clientIds[i])
			}

			// wait for watch done
			allWaitGroup.Wait()
			duration += time.Since(start)

			fmt.Printf("Test %s succeed! Total duration %v\n", tt.name, duration)
		})
	}
}

/*
RV using map, has lock on ResourceDistributor.ProcessEvents
Processing 20 AddNode events took 47.672µs.
Processing 200 AddNode events took 180.329µs.
Processing 2000 AddNode events took 1.639455ms.
Processing 20000 AddNode events took 15.005638ms.
Processing 200000 AddNode events took 202.639629ms.
Processing 2000000 AddNode events took 2.202974335s.

RV using map, NO lock on ResourceDistributor.ProcessEvents
Processing 20 AddNode events took 49.985µs.
Processing 200 AddNode events took 177.707µs.
Processing 2000 AddNode events took 1.6575ms.
Processing 20000 AddNode events took 15.87493ms.
Processing 200000 AddNode events took 203.718498ms.
Processing 2000000 AddNode events took 2.338442347s.

RV using array, NO lock on ResourceDistributor.ProcessEvents, has lock on rv check
Processing 20 AddNode events took 71.154µs.
Processing 200 AddNode events took 183.098µs.
Processing 2000 AddNode events took 1.607259ms.
Processing 20000 AddNode events took 11.793391ms.
Processing 200000 AddNode events took 154.692465ms.
Processing 2000000 AddNode events took 1.85247087s.

RV using array, added lock for snapshot in event queue
Processing 20 AddNode events took 46.419µs.
Processing 200 AddNode events took 144.9µs.
Processing 2000 AddNode events took 1.390383ms.
Processing 20000 AddNode events took 11.794798ms.
Processing 200000 AddNode events took 144.229808ms.
Processing 2000000 AddNode events took 1.86103252s.
*/
func TestProcessEvents_TwoRPs_AddNodes_Sequential(t *testing.T) {
	distributor := setUp()
	defer tearDown()

	nodeCounts := []int{10, 100, 1000, 10000, 100000, 1000000}
	// generate add node events
	for i := 0; i < len(nodeCounts); i++ {
		eventsAdd1 := generateAddNodeEvent(nodeCounts[i], defaultLocBeijing_RP1)
		eventsAdd2 := generateAddNodeEvent(nodeCounts[i], location.NewLocation(location.Shanghai, location.ResourcePartition2))
		start := time.Now()
		distributor.ProcessEvents(eventsAdd1)
		_, rvMap := distributor.ProcessEvents(eventsAdd2)
		duration := time.Since(start)
		fmt.Printf("Processing %d AddNode events took %v. Composite RVs %v\n", nodeCounts[i]*2, duration, rvMap)
	}
}

/*
RV using map, has lock on ResourceDistributor.ProcessEvents
Processing 20 AddNode events took 114.303µs.
Processing 200 AddNode events took 232.242µs.
Processing 2000 AddNode events took 2.335808ms.
Processing 20000 AddNode events took 15.309473ms.
Processing 200000 AddNode events took 182.779783ms.
Processing 2000000 AddNode events took 2.251335613s.

RV using map, NO lock on ResourceDistributor.ProcessEvents
Processing 20 AddNode events took 77.699µs.
Processing 200 AddNode events took 188.916µs.
Processing 2000 AddNode events took 1.206867ms.
Processing 20000 AddNode events took 11.0227ms.
Processing 200000 AddNode events took 126.118044ms.
Processing 2000000 AddNode events took 1.568443698s.

RV using array, NO lock on ResourceDistributor.ProcessEvents, has lock on rv check
Processing 20 AddNode events took 70.499µs.
Processing 200 AddNode events took 167.544µs.
Processing 2000 AddNode events took 906.622µs.
Processing 20000 AddNode events took 6.965772ms.
Processing 200000 AddNode events took 106.936265ms.
Processing 2000000 AddNode events took 1.24163592s.

RV using array, added lock for snapshot in event queue
Processing 20 AddNode events took 87.807µs.
Processing 200 AddNode events took 177.349µs.
Processing 2000 AddNode events took 859.874µs.
Processing 20000 AddNode events took 6.772558ms.
Processing 200000 AddNode events took 109.476743ms.
Processing 2000000 AddNode events took 1.21830443s.
*/
func TestProcessEvents_TwoRPs_Concurrent(t *testing.T) {
	distributor := setUp()
	defer tearDown()

	nodeCounts := []int{10, 100, 1000, 10000, 100000, 1000000}
	// generate add node events
	for i := 0; i < len(nodeCounts); i++ {
		eventsAdd1 := generateAddNodeEvent(nodeCounts[i], defaultLocBeijing_RP1)
		eventsAdd2 := generateAddNodeEvent(nodeCounts[i], location.NewLocation(location.Shanghai, location.ResourcePartition2))
		start := time.Now()

		wg := &sync.WaitGroup{}
		wg.Add(2)

		go func(done *sync.WaitGroup, eventsToProcess []*event.NodeEvent) {
			distributor.ProcessEvents(eventsToProcess)
			done.Done()
		}(wg, eventsAdd1)

		go func(done *sync.WaitGroup, eventsToProcess []*event.NodeEvent) {
			distributor.ProcessEvents(eventsToProcess)
			done.Done()
		}(wg, eventsAdd2)

		wg.Wait()
		duration := time.Since(start)
		fmt.Printf("Processing %d AddNode events took %v.\n", nodeCounts[i]*2, duration)
	}
}