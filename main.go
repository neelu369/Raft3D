package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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

func printersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
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

		applyFuture := node.raft.Apply(cmdBytes, 5*time.Second)
		if err := applyFuture.Error(); err != nil {
			http.Error(w, "Raft apply failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "Printer %s added successfully\n", printer.ID)

	case http.MethodGet:
		node.fsm.mu.Lock()
		defer node.fsm.mu.Unlock()

		printers := make([]Printer, 0, len(node.fsm.Printers))
		for _, p := range node.fsm.Printers {
			printers = append(printers, p)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(printers)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
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

func filamentHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var filament Filament
		if err := json.NewDecoder(r.Body).Decode(&filament); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		data, err := json.Marshal(filament)
		if err != nil {
			http.Error(w, "Failed to serialize filament", http.StatusInternalServerError)
			return
		}

		cmd := Command{
			Op:   "add_filament",
			Data: data,
		}

		cmdBytes, err := json.Marshal(cmd)
		if err != nil {
			http.Error(w, "Failed to serialize command", http.StatusInternalServerError)
			return
		}

		applyFuture := node.raft.Apply(cmdBytes, 5*time.Second)
		if err := applyFuture.Error(); err != nil {
			http.Error(w, "Raft apply failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "Filament %s added successfully\n", filament.ID)

	case http.MethodGet:
		node.fsm.mu.Lock()
		defer node.fsm.mu.Unlock()

		filaments := make([]Filament, 0, len(node.fsm.Filaments))
		for _, f := range node.fsm.Filaments {
			filaments = append(filaments, f)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(filaments)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

const raftTimeout = 5 * time.Second

func handlePostPrintJob(raftNode *raft.Raft, fsm *RaftFSM) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var job PrintJob
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if _, ok := fsm.Printers[job.PrinterID]; !ok {
			http.Error(w, "Invalid printer_id", http.StatusBadRequest)
			return
		}
		filament, ok := fsm.Filaments[job.FilamentID]
		if !ok {
			http.Error(w, "Invalid filament_id", http.StatusBadRequest)
			return
		}

		usedWeight := 0
		for _, pj := range fsm.PrintJobs {
			if pj.FilamentID == job.FilamentID && (pj.Status == "Queued" || pj.Status == "Running") {
				usedWeight += pj.PrintWeightInGrams
			}
		}
		if job.PrintWeightInGrams > (filament.RemainingWeightInGrams - usedWeight) {
			http.Error(w, "Not enough filament available", http.StatusBadRequest)
			return
		}
		if job.PrintWeightInGrams <= 0 {
			http.Error(w, "Print weight must be greater than zero", http.StatusBadRequest)
			return
		}

		job.Status = "Queued"
		job.ID = uuid.New().String()

		cmd := Command{
			Op:   "add_print_job",
			Data: mustMarshal(job),
		}
		if err := applyRaftCommand(raftNode, cmd); err != nil {
			http.Error(w, "Raft apply failed: node is not the leader", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(job)
	}
}

func handleGetPrintJobs(fsm *RaftFSM) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var jobs []PrintJob
		for _, job := range fsm.PrintJobs {
			jobs = append(jobs, job)
		}
		json.NewEncoder(w).Encode(jobs)
	}
}

func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
func applyRaftCommand(r *raft.Raft, cmd Command) error {
	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	f := r.Apply(b, raftTimeout)
	return f.Error()
}

func handleUpdatePrintJobStatus(raftNode *raft.Raft, fsm *RaftFSM) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		segments := strings.Split(r.URL.Path, "/")
		if len(segments) < 3 {
			http.Error(w, "Invalid URL path", http.StatusBadRequest)
			return
		}
		jobID := segments[len(segments)-2]
		statusParam := r.URL.Query().Get("status")
		if statusParam == "" {
			http.Error(w, "Missing status parameter", http.StatusBadRequest)
			return
		}

		if statusParam != "running" && statusParam != "done" && statusParam != "canceled" {
			http.Error(w, "Invalid status. Must be 'running', 'done', or 'canceled'", http.StatusBadRequest)
			return
		}

		fsm.mu.Lock()
		job, exists := fsm.PrintJobs[jobID]
		if !exists {
			fsm.mu.Unlock()
			http.Error(w, "Print job not found", http.StatusNotFound)
			return
		}
		currentStatus := job.Status
		fsm.mu.Unlock() // defer behaving strange, re-look

		validTransition := false // start with F instead of T
		switch statusParam {
		case "running":
			validTransition = currentStatus == "Queued"
		case "done":
			validTransition = currentStatus == "Running"
		case "canceled":
			validTransition = currentStatus == "Queued" || currentStatus == "Running" // Last case
		}

		if !validTransition {
			http.Error(w, fmt.Sprintf("Invalid status transition from '%s' to '%s'", currentStatus, statusParam), http.StatusBadRequest)
			return
		}

		type StatusUpdate struct {
			JobID  string `json:"job_id"`
			Status string `json:"status"`
		}

		update := StatusUpdate{
			JobID:  jobID,
			Status: statusParam,
		}

		cmd := Command{
			Op:   "update_print_job_status",
			Data: mustMarshal(update),
		}

		if err := applyRaftCommand(raftNode, cmd); err != nil {
			http.Error(w, "Failed to update job status", http.StatusInternalServerError)
			return
		}

		fsm.mu.Lock() // Sec Lock
		updatedJob := fsm.PrintJobs[jobID]
		fsm.mu.Unlock()

		w.Header().Set("Content-Type", "application/json") // *
		json.NewEncoder(w).Encode(updatedJob)
	}
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
	http.HandleFunc("/printers", printersHandler)
	http.HandleFunc("/view_stats", viewStats)
	http.HandleFunc("/leader", leaderHandler)
	http.HandleFunc("/filaments", filamentHandler)
	http.HandleFunc("/print_jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			handleGetPrintJobs(node.fsm)(w, r)
		case "POST":
			handlePostPrintJob(node.raft, node.fsm)(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/print_jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/status") && r.Method == "POST" {
			handleUpdatePrintJobStatus(node.raft, node.fsm)(w, r)
		} else if path == "/print_jobs" {
			switch r.Method {
			case "GET":
				handleGetPrintJobs(node.fsm)(w, r)
			case "POST":
				handlePostPrintJob(node.raft, node.fsm)(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})

	fmt.Printf("HTTP server starting on :%s ...\n", *httpPort)
	err = http.ListenAndServe(":"+*httpPort, nil)
	if err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

// --- done ---
