package api

// ConsistencyLevel represents the available read consistency levels
type ConsistencyLevel int

// Consistency level values
const (
	// Read direct from db at any node
	ReadAny ConsistencyLevel = iota
	// Read direct from db at leader node
	ReadLeader
	// Read linearizable from leader node
	ReadRaft
)

// Client is a client interface to rkv
type Client interface {
	Put(tab, key, value []byte) error
	Get(level ConsistencyLevel, tab, key []byte) ([]byte, error)
	Delete(tab, key []byte) error
	Shutdown() error
}
