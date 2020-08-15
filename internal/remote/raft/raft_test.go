package raft_test

import (
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"testing"
	"time"

	rkvApi "github.com/tdx/rkv/api"
	dbApi "github.com/tdx/rkv/db/api"
	"github.com/tdx/rkv/db/bitcask"
	"github.com/tdx/rkv/db/bolt"
	"github.com/tdx/rkv/db/gmap"
	rRaft "github.com/tdx/rkv/internal/remote/raft"

	log "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
)

func TestNodesBolt(t *testing.T) {
	run(t, "bolt")
}

func TestNodesMap(t *testing.T) {
	run(t, "map")
}

func TestNodesBitcask(t *testing.T) {
	run(t, "bitcask")
}

func run(t *testing.T, bkType string) {
	var nodes []*rRaft.Backend
	nodeCount := 3
	ports := dynaport.Get(nodeCount)

	for i := 0; i < nodeCount; i++ {
		dataDir, err := ioutil.TempDir("", "raft-db-test")
		require.NoError(t, err)

		defer func(dir string) {
			_ = os.RemoveAll(dir)
		}(dataDir)

		ln, err := net.Listen(
			"tcp",
			fmt.Sprintf("127.0.0.1:%d", ports[i]),
		)

		require.NoError(t, err)

		config := &rRaft.Config{}
		config.StreamLayer = rRaft.NewStreamLayer(ln)
		config.Raft.LocalID = raft.ServerID(fmt.Sprintf("%d", i))
		config.Raft.HeartbeatTimeout = 50 * time.Millisecond
		config.Raft.ElectionTimeout = 50 * time.Millisecond
		config.Raft.LeaderLeaseTimeout = 50 * time.Millisecond
		config.Raft.CommitTimeout = 5 * time.Millisecond

		if i == 0 {
			config.Bootstrap = true
		}

		var db dbApi.Backend
		switch bkType {
		case "map":
			db, err = gmap.New(dataDir)
		case "bitcask":
			db, err = bitcask.New(dataDir, 1<<20) // 1 MB
		default:
			db, err = bolt.New(dataDir)
		}
		require.NoError(t, err)

		node, err := rRaft.New(db, config)
		require.NoError(t, err)

		if i != 0 {
			err = nodes[0].Join(fmt.Sprintf("%d", i), ln.Addr().String())
		} else {
			err = node.WaitForLeader(3 * time.Second)
		}
		require.NoError(t, err)

		nodes = append(nodes, node)
	}

	var (
		tab  = []byte{'t', 'a', 'b'}
		key1 = []byte{'k', 'e', 'y', '1'}
		key2 = []byte{'k', 'e', 'y', '2'}
		key3 = []byte{'k', 'e', 'y', '3'}
		val1 = []byte{'v', 'a', 'l', '1'}
		val2 = []byte{'v', 'a', 'l', '2'}
		val3 = []byte{'v', 'a', 'l', '3'}
	)

	records := []struct {
		Key []byte
		Val []byte
	}{
		{Key: key1, Val: val1},
		{Key: key2, Val: val2},
	}

	for _, record := range records {
		err := nodes[0].Put(tab, record.Key, record.Val)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			for j := 0; j < nodeCount; j++ {
				got, err := nodes[j].Get(rkvApi.ReadAny, tab, record.Key)
				if err != nil {
					return false
				}
				require.Equal(t, record.Val, got)
			}
			return true
		}, 500*time.Millisecond, 50*time.Millisecond)
	}

	// remove node "1" from cluster
	err := nodes[0].Leave("1", "")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	err = nodes[0].Put(tab, key3, val3)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// not a leader, use ReadAny
	v3, err := nodes[1].Get(rkvApi.ReadAny, tab, key3)
	t.Log("v3:", v3, "err:", err)
	require.Equal(t, true, dbApi.IsNoKeyError(err))
	require.Nil(t, v3)

	v3, err = nodes[2].Get(rkvApi.ReadAny, tab, key3)
	require.NoError(t, err)
	require.Equal(t, val3, v3)

	//
	// ERROR: leadership lost while committing log
	//
	// // add node "1" to cluster again
	// err = nodes[0].Join("1", nodes[1].Addr().String())
	// require.NoError(t, err)

	// // wait to replicate ?
	// time.Sleep(50 * time.Millisecond)

	// v3, err = nodes[1].Get(tab, key3)
	// require.NoError(t, err)
	// require.Equal(t, val3, v3)
}

//
// restart existing cluster
//

// Need call 2 times
func TestRestartWithState(t *testing.T) {
	run2(t, "map")
}

func run2(t *testing.T, bkType string) {
	var (
		dbs  []dbApi.Backend
		cfgs []*rRaft.Config

		nodeCount = 3
		ports     = []int{12000, 12001, 12002}
	)

	for i := 0; i < nodeCount; i++ {
		dataDir := fmt.Sprintf("/tmp/raft-test/%d", i)

		ln, err := net.Listen(
			"tcp",
			fmt.Sprintf("127.0.0.1:%d", ports[i]),
		)

		require.NoError(t, err)

		cfg := &rRaft.Config{}
		cfg.StreamLayer = rRaft.NewStreamLayer(ln)
		cfg.Raft.LocalID = raft.ServerID(fmt.Sprintf("%d", i))
		cfg.Raft.HeartbeatTimeout = 50 * time.Millisecond
		cfg.Raft.ElectionTimeout = 50 * time.Millisecond
		cfg.Raft.LeaderLeaseTimeout = 50 * time.Millisecond
		cfg.Raft.CommitTimeout = 5 * time.Millisecond

		cfg.Raft.Logger = log.New(&log.LoggerOptions{
			Level: log.Trace,
		})

		// allow node 0 start as leader
		if i != 0 {
			cfg.Raft.HeartbeatTimeout *= 5
			cfg.Raft.ElectionTimeout *= 5
		}

		if i == 0 {
			cfg.Bootstrap = true
			cfg.Raft.StartAsLeader = true
		}

		var db dbApi.Backend
		switch bkType {
		case "map":
			db, err = gmap.New(dataDir)
		case "bitcask":
			db, err = bitcask.New(dataDir, 1<<20) // 1 MB
		default:
			db, err = bolt.New(dataDir)
		}
		require.NoError(t, err)

		dbs = append(dbs, db)
		cfgs = append(cfgs, cfg)
	}

	leader, err := rRaft.New(dbs[0], cfgs[0])
	require.NoError(t, err)

	restarted := leader.Restarted()
	t.Log("---------------", restarted, "---------------")

	if !restarted {
		err = leader.WaitForLeader(3 * time.Second)
		require.NoError(t, err)
	}

	//
	// node 2
	//
	node2, err := rRaft.New(dbs[2], cfgs[2])
	require.NoError(t, err)

	if !restarted {
		localID := "2"
		addr := node2.Addr().String()

		t.Log("join:", localID, addr)

		err = leader.Join(localID, addr)
		require.NoError(t, err)
	}

	// wait for cluster with node 2
	if restarted {
		err = leader.WaitForLeader(3 * time.Second)
		require.NoError(t, err)
	}

	//
	// node 1
	//
	node1, err := rRaft.New(dbs[1], cfgs[1])
	require.NoError(t, err)

	// always join node 1
	localID := "1"
	addr := node1.Addr().String()

	t.Log("join:", localID, addr)

	err = leader.Join(localID, addr)
	require.NoError(t, err)

	var (
		tab  = []byte{'t', 'a', 'b'}
		key1 = []byte{'k', 'e', 'y', '1'}
		key2 = []byte{'k', 'e', 'y', '2'}
		val1 = []byte{'v', 'a', 'l', '1'}
		val2 = []byte{'v', 'a', 'l', '2'}
		val3 = []byte{'v', 'a', 'l', '3'}
	)

	records := []struct {
		Key []byte
		Val []byte
	}{
		{Key: key1, Val: val1},
		{Key: key2, Val: val2},
	}

	var nodes []*rRaft.Backend
	nodes = append(nodes, leader)
	nodes = append(nodes, node1)
	nodes = append(nodes, node2)

	for _, record := range records {
		if !restarted {
			err := leader.Put(tab, record.Key, record.Val)
			require.NoError(t, err)
		}

		require.Eventually(t, func() bool {
			for j := 0; j < nodeCount; j++ {
				got, err := nodes[j].Get(rkvApi.ReadAny, tab, record.Key)
				if err != nil {
					return false
				}
				require.Equal(t, record.Val, got)
			}
			return true
		}, 500*time.Millisecond, 50*time.Millisecond)
	}

	// remove node "1" from cluster
	err = leader.Leave("1", "")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// generate random key, val
	newKey := make([]byte, 10)
	_, err = rand.Read(newKey)
	require.NoError(t, err)

	err = leader.Put(tab, newKey, val3)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// not a leader, use ReadAny
	v3, err := node1.Get(rkvApi.ReadAny, tab, newKey)
	t.Log("v3:", v3, "err:", err)
	require.Equal(t, true, dbApi.IsNoKeyError(err))
	require.Nil(t, v3)

	v3, err = node2.Get(rkvApi.ReadAny, tab, newKey)
	require.NoError(t, err)
	require.Equal(t, val3, v3)
}
