package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	// "strconv"
	// "strings"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
)

var mu sync.Mutex

type raftNode struct {
	raft *raft.Raft
	fsm  *RaftFSM
}

var node *raftNode

func newRaftNode(id, raftAddr, dataDir string, bootstrap bool) (*raftNode, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(id)

	// Log + stable store
	store, err := raftboltdb.NewBoltStore(dataDir + "/raft.db")
	if err != nil {
		return nil, err
	}

	// Snapshot store
	snapshots, err := raft.NewFileSnapshotStore(dataDir, 2, os.Stderr)
	if err != nil {
		return nil, err
	}

	// TCP transport
	transport, err := raft.NewTCPTransport(raftAddr, nil, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, err
	}

	// FSM
	fsm := NewFSM()

	// Create raft instance
	r, err := raft.NewRaft(config, fsm, store, store, snapshots, transport)
	if err != nil {
		return nil, err
	}

	if bootstrap {
		cfg := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      raft.ServerID(id),
					Address: transport.LocalAddr(),
				},
			},
		}
		r.BootstrapCluster(cfg)
	}

	return &raftNode{raft: r, fsm: fsm}, nil
}

// Root test handler
func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Raft3D Node is running!")
}

// /join handler for adding followers dynamically
func joinHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type JoinRequest struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}

	var req JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	configFuture := node.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		http.Error(w, "Failed to get Raft config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Check if already added
	for _, srv := range configFuture.Configuration().Servers {
		if srv.ID == raft.ServerID(req.ID) || srv.Address == raft.ServerAddress(req.Address) {
			fmt.Fprintf(w, "Node %s at %s already joined\n", req.ID, req.Address)
			return
		}
	}

	future := node.raft.AddVoter(raft.ServerID(req.ID), raft.ServerAddress(req.Address), 0, 0)
	if err := future.Error(); err != nil {
		http.Error(w, "Failed to add voter: "+err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "Node %s at %s joined successfully!\n", req.ID, req.Address)
}

// POST /printers
func addPrinterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var printer Printer
	if err := json.NewDecoder(r.Body).Decode(&printer); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	data, err := json.Marshal(printer)
	if err != nil {
		http.Error(w, "Failed to serialize printer", http.StatusInternalServerError)
		return
	}

	cmd := Command{
		Op:   "add_printer",
		Data: data,
	}

	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		http.Error(w, "Failed to serialize command", http.StatusInternalServerError)
		return
	}

	// Submit command via Raft
	applyFuture := node.raft.Apply(cmdBytes, 5*time.Second)
	if err := applyFuture.Error(); err != nil {
		http.Error(w, "Raft apply failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "Printer %s added successfully\n", printer.ID)
}

// GET /printers
func getPrintersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node.fsm.mu.Lock()
	defer node.fsm.mu.Unlock()

	printers := make([]Printer, 0, len(node.fsm.Printers))
	for _, p := range node.fsm.Printers {
		printers = append(printers, p)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(printers)
}

func leaderHandler(w http.ResponseWriter, r *http.Request) {
	leader := node.raft.Leader()
	if leader == "" {
		http.Error(w, "No leader elected yet", http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintf(w, "%s\n", leader)
}

func viewStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	leader := node.raft.Leader()
	if leader == "" {
		http.Error(w, "No leader elected yet", http.StatusServiceUnavailable)
		return
	}

	// Get basic Raft status information
	state := node.raft.State()
	stats := node.raft.Stats()

	type LeaderInfo struct {
		Leader      string `json:"leader"`
		CurrentTerm string `json:"term"`
		State       string `json:"state"`
		IsLeader    bool   `json:"is_leader"`
	}

	response := LeaderInfo{
		Leader:      string(leader),
		CurrentTerm: stats["term"],
		State:       state.String(),
		IsLeader:    (state == raft.Leader),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	id := flag.String("id", "node1", "Unique ID of this node")
	raftPort := flag.String("raftPort", "12000", "Raft communication port")
	httpPort := flag.String("httpPort", "8080", "HTTP server port")
	dataDir := flag.String("dataDir", "./data", "Directory to store Raft logs")
	bootstrap := flag.Bool("bootstrap", false, "Bootstrap this node as the initial cluster leader")
	flag.Parse()

	raftAddr := fmt.Sprintf("127.0.0.1:%s", *raftPort)
	fmt.Printf("Starting node %s with Raft Addr %s and HTTP Port %s\n", *id, raftAddr, *httpPort)

	var err error
	node, err = newRaftNode(*id, raftAddr, *dataDir, *bootstrap)
	if err != nil {
		log.Fatalf("Failed to start raft node: %v", err)
	}

	http.HandleFunc("/", handler)
	http.HandleFunc("/join", joinHandler)
	http.HandleFunc("/printers", addPrinterHandler)      // POST
	http.HandleFunc("/get_printers", getPrintersHandler) // GET
	http.HandleFunc("/view_stats", viewStats)
	http.HandleFunc("/leader", leaderHandler)

	fmt.Printf("HTTP server starting on :%s ...\n", *httpPort)
	err = http.ListenAndServe(":"+*httpPort, nil)
	if err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
