package main

import (
	"encoding/json"
	"io"
	"log"
	"sync"

	"github.com/hashicorp/raft"
)

type RaftFSM struct {
	mu        sync.Mutex
	Printers  map[string]Printer
	Filaments map[string]Filament
	PrintJobs map[string]PrintJob
}

// --- FSM Initialization ---

func NewFSM() *RaftFSM {
	return &RaftFSM{
		Printers:  make(map[string]Printer),
		Filaments: make(map[string]Filament),
		PrintJobs: make(map[string]PrintJob),
	}
}

// --- Raft FSM Required Methods ---

func (f *RaftFSM) Apply(logEntry *raft.Log) interface{} {
	var cmd Command
	if err := json.Unmarshal(logEntry.Data, &cmd); err != nil {
		log.Println("FSM apply error: could not decode command:", err)
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch cmd.Op {
	case "add_printer":
		var printer Printer
		if err := json.Unmarshal(cmd.Data, &printer); err == nil {
			f.Printers[printer.ID] = printer
		}
	case "add_filament":
		var filament Filament
		if err := json.Unmarshal(cmd.Data, &filament); err == nil {
			f.Filaments[filament.ID] = filament
		}
	case "add_print_job":
		var job PrintJob
		if err := json.Unmarshal(cmd.Data, &job); err != nil {
			return nil
		}
		f.PrintJobs[job.ID] = job
		return nil
	}

	return nil
}

func (f *RaftFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Clone current state
	state := map[string]interface{}{
		"printers":  f.Printers,
		"filaments": f.Filaments,
		"printjobs": f.PrintJobs,
	}
	buf, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return &fsmSnapshot{state: buf}, nil
}

func (f *RaftFSM) Restore(snapshot io.ReadCloser) error {
	var state map[string]interface{}
	decoder := json.NewDecoder(snapshot)
	if err := decoder.Decode(&state); err != nil {
		return err
	}

	// Convert back to proper types
	printersJSON, _ := json.Marshal(state["printers"])
	filamentsJSON, _ := json.Marshal(state["filaments"])
	printJobsJSON, _ := json.Marshal(state["printjobs"])

	var printers map[string]Printer
	var filaments map[string]Filament
	var printJobs map[string]PrintJob

	json.Unmarshal(printersJSON, &printers)
	json.Unmarshal(filamentsJSON, &filaments)
	json.Unmarshal(printJobsJSON, &printJobs)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.Printers = printers
	f.Filaments = filaments
	f.PrintJobs = printJobs

	return nil
}

// --- Snapshot implementation ---

type fsmSnapshot struct {
	state []byte
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	_, err := sink.Write(s.state)
	if err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}
