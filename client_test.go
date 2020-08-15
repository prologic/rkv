package rkv_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/tdx/rkv"
	"github.com/tdx/rkv/api"
	dbApi "github.com/tdx/rkv/db/api"
	"github.com/tdx/rkv/db/bitcask"
	"github.com/tdx/rkv/db/bolt"
	"github.com/tdx/rkv/db/gmap"

	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
)

func TestClientBolt(t *testing.T) {
	run(t, "bolt")
}

func TestClientMap(t *testing.T) {
	run(t, "map")
}

func TestClientBitcask(t *testing.T) {
	run(t, "bitcask")
}

func run(t *testing.T, bkType string) {
	dataDir, err := ioutil.TempDir("", "client-test-*")
	require.NoError(t, err)

	defer os.RemoveAll(dataDir)

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

	ports := dynaport.Get(2)

	config := &api.Config{
		Backend:       db,
		NodeName:      "1",
		LogLevel:      "debug",
		DiscoveryAddr: fmt.Sprintf("127.0.0.1:%d", ports[0]),
		RaftPort:      ports[1],
	}

	client, err := rkv.NewClient(config)
	require.NoError(t, err)

	defer client.Shutdown()

	var (
		tab = []byte{'t', 'a', 'b'}
		key = []byte{'k', 'e', 'y'}
		val = []byte{'v', 'a', 'l'}
	)

	err = client.Put(tab, key, val)
	require.NoError(t, err)

	v, err := client.Get(api.ReadAny, tab, key)
	require.Equal(t, val, v)

	err = client.Delete(tab, key)
	require.NoError(t, err)

	v, err = client.Get(api.ReadRaft, tab, key)
	t.Log("v:", v, "err:", err)
	require.Equal(t, true, dbApi.IsNoKeyError(err))
	require.Nil(t, v)

}
