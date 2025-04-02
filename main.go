package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"github.com/hashicorp/raft-boltdb"
)

// Mutex for thread safety
var mu sync.Mutex

// FSM (Finite State Machine) for Raft state management
type fsm struct {
	data map[string]string
	mu   sync.Mutex
}

// Apply log entry to the FSM
func (f *fsm) Apply(log *raft.Log) interface{} {
	var command map[string]string
	if err := json.Unmarshal(log.Data, &command); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range command {
		f.data[k] = v
	}
	return nil
}

// Snapshot (not needed for simple KV store)
func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	return nil, nil
}

// Restore state from snapshot (not needed here)
func (f *fsm) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	return nil
}

// RaftNode structure
type raftNode struct {
	raft *raft.Raft
	fsm  *fsm
}

// Create a new Raft Node
func newRaftNode(dataDir string, id string, peers []raft.Server) (*raftNode, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(id)

	// Storage for Raft logs
	store, err := raftboltdb.NewBoltStore(dataDir + "/raft.db")
	if err != nil {
		return nil, err
	}

	// Snapshot storage
	snapshots, err := raft.NewFileSnapshotStore(dataDir, 1, os.Stderr)
	if err != nil {
		return nil, err
	}

	// Transport setup
	addr := "127.0.0.1:12000"
	transport, err := raft.NewTCPTransport(addr, nil, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, err
	}

	// Initialize FSM
	fsmInstance := &fsm{data: make(map[string]string)}

	// Initialize Raft
	raftInstance, err := raft.NewRaft(config, fsmInstance, store, store, snapshots, transport)
	if err != nil {
		return nil, err
	}

	// Bootstrap cluster if needed
	configuration := raft.Configuration{Servers: peers}
	raftInstance.BootstrapCluster(configuration)

	return &raftNode{raft: raftInstance, fsm: fsmInstance}, nil
}

var node *raftNode

// HTTP handler to set key-value pairs
func setHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	body, _ := ioutil.ReadAll(r.Body)
	var data map[string]string
	json.Unmarshal(body, &data)

	// Serialize and commit the command to Raft
	command, _ := json.Marshal(data)
	future := node.raft.Apply(command, 5*time.Second)
	if err := future.Error(); err != nil {
		http.Error(w, "Failed to apply command", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Key-Value stored successfully")
}

// HTTP handler to get values by key
func getHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	key := r.URL.Query().Get("key")
	node.fsm.mu.Lock()
	value, exists := node.fsm.data[key]
	node.fsm.mu.Unlock()

	if !exists {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	response := map[string]string{"key": key, "value": value}
	json.NewEncoder(w).Encode(response)
}

// Simple handler for the root path
func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Hello, RAFT!")
}

func main() {
	peers := []raft.Server{
		{ID: "node1", Address: "127.0.0.1:12000"},
	}

	// Initialize Raft Node
	var err error
	node, err = newRaftNode("./data", "node1", peers)
	if err != nil {
		log.Fatal("Failed to create Raft node:", err)
	}

	// HTTP server setup
	http.HandleFunc("/", handler)  // Root endpoint
	http.HandleFunc("/set", setHandler)
	http.HandleFunc("/get", getHandler)

	fmt.Println("Starting HTTP Server on port 8080...")
	
	// Explicitly log any error that occurs
	err = http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
	}
}
