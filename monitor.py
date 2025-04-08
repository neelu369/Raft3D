#!/usr/bin/env python3

import argparse
import json
import os
import requests
import time
from datetime import datetime
from tabulate import tabulate

class Node:
    def __init__(self, id, http_addr, raft_addr):
        self.id = id
        self.http_addr = http_addr
        self.raft_addr = raft_addr
        self.is_leader = False
        self.is_healthy = False
        self.stats = "-"

class ClusterMonitor:
    def __init__(self, refresh_rate=5, pretty_output=True):
        self.nodes = {}  # Using a dict to avoid duplicates
        self.refresh_rate = refresh_rate
        self.pretty_output = pretty_output
        self.leader_raft_addr = None

    def add_node(self, id, http_addr, raft_addr):
        if id not in self.nodes:
            self.nodes[id] = Node(id, http_addr, raft_addr)
            return True
        return False

    def check_node_health(self, node):
        try:
            url = f"http://{node.http_addr}"
            response = requests.get(url, timeout=1)
            return response.status_code == 200
        except requests.RequestException:
            return False

    def get_leader_address(self, node):
        try:
            url = f"http://{node.http_addr}/leader"
            response = requests.get(url, timeout=1)
            if response.status_code == 200:
                return response.text.strip()
            return None
        except requests.RequestException:
            return None

    def get_node_raft_address(self, http_addr):
        """Try to determine the correct Raft address for a node based on HTTP port pattern"""
        parts = http_addr.split(':')
        host = parts[0]
        http_port = int(parts[1])
        
        # The pattern appears to be: HTTP port 8080 → Raft port 12000
        raft_port = 12000 + (http_port - 8080)
        return f"127.0.0.1:{raft_port}"

    def discover_cluster_configuration(self):
        """Dynamically discover all nodes in the cluster by querying known nodes"""
        # Start with a seed node on the default port
        seed_http_ports = [8080, 8081, 8082, 8083, 8084]  # Try a few common ports
        seed_found = False
        
        for port in seed_http_ports:
            seed_http_addr = f"localhost:{port}"
            try:
                # Check if the seed node is alive
                response = requests.get(f"http://{seed_http_addr}", timeout=1)
                if response.status_code == 200:
                    # Use a default ID for now
                    seed_id = f"node{port-8079}"  # Assuming node1 is on 8080, node2 on 8081, etc.
                    seed_raft_addr = self.get_node_raft_address(seed_http_addr)
                    self.add_node(seed_id, seed_http_addr, seed_raft_addr)
                    seed_found = True
                    print(f"Found seed node at {seed_http_addr} with Raft address {seed_raft_addr}")
            except requests.RequestException:
                continue
        
        if not seed_found:
            print("No seed node found. Make sure at least one Raft node is running.")
            return False
        
        # Now get the leader from any working node
        for node in list(self.nodes.values()):
            if self.check_node_health(node):
                leader_addr = self.get_leader_address(node)
                if leader_addr:
                    self.leader_raft_addr = leader_addr
                    print(f"Discovered leader at {leader_addr}")
                    break
        
        return len(self.nodes) > 0

    def get_cluster_stats(self, node):
        try:
            url = f"http://{node.http_addr}/get_printers"
            response = requests.get(url, timeout=1)
            if response.status_code == 200:
                printers = json.loads(response.text)
                return f"Printers: {len(printers)}"
            return "-"
        except requests.RequestException:
            return "-"
        except json.JSONDecodeError:
            return "Error: Invalid JSON"

    def refresh_status(self):
        """Update the status of all known nodes and potentially discover new ones"""
        # Scan for new nodes first on adjacent ports
        known_http_ports = [int(node.http_addr.split(':')[1]) for node in self.nodes.values()]
        
        # Check a range around known ports
        for base_port in known_http_ports:
            for port_offset in range(-2, 3):
                port = base_port + port_offset
                if port <= 0 or port in known_http_ports:
                    continue
                
                http_addr = f"localhost:{port}"
                try:
                    response = requests.get(f"http://{http_addr}", timeout=0.5)
                    if response.status_code == 200:
                        node_id = f"node{port-8079}"  # Assuming node1 is on 8080, node2 on 8081, etc.
                        raft_addr = self.get_node_raft_address(http_addr)
                        if self.add_node(node_id, http_addr, raft_addr):
                            print(f"Discovered new node at {http_addr} with Raft address {raft_addr}")
                except requests.RequestException:
                    pass
        
        # First get the current leader address from any healthy node
        for node in self.nodes.values():
            if self.check_node_health(node):
                leader_addr = self.get_leader_address(node)
                if leader_addr:
                    self.leader_raft_addr = leader_addr
                    break
        
        # Now update all nodes
        for node in self.nodes.values():
            node.is_healthy = self.check_node_health(node)
            if node.is_healthy:
                # Set leader flag if this node's Raft address matches the leader address
                node.is_leader = (node.raft_addr == self.leader_raft_addr)
                node.stats = self.get_cluster_stats(node)
            else:
                node.is_leader = False
                node.stats = "-"

    def clear_screen(self):
        os.system('cls' if os.name == 'nt' else 'clear')

    def display(self):
        self.clear_screen()
        print(f"Raft Cluster Monitor - {datetime.now().strftime('%a %b %d %H:%M:%S %Y')}\n")
        print(f"Monitoring {len(self.nodes)} nodes")
        
        # Find the leader node
        leader_node = next((n for n in self.nodes.values() if n.is_leader), None)
        
        if leader_node:
            print(f"Current leader: {leader_node.id} ({leader_node.http_addr})")
        elif self.leader_raft_addr:
            print(f"Current leader: Unknown ({self.leader_raft_addr})")
        else:
            print("No leader detected")
        
        print()
        
        if self.pretty_output:
            headers = ["ID", "HTTP Address", "Raft Address", "Health", "Role", "Stats"]
            rows = []
            
            # Sort nodes by ID for consistent display
            sorted_nodes = sorted(self.nodes.values(), key=lambda n: n.id)
            
            for node in sorted_nodes:
                health = "UP" if node.is_healthy else "DOWN"
                role = "LEADER" if node.is_leader else "Follower"
                rows.append([node.id, node.http_addr, node.raft_addr, health, role, node.stats])
            
            print(tabulate(rows, headers=headers, tablefmt="grid"))
        else:
            for node_id in sorted(self.nodes.keys()):
                node = self.nodes[node_id]
                health = "UP" if node.is_healthy else "DOWN"
                role = "LEADER" if node.is_leader else "Follower"
                print(f"Node {node.id} ({node.http_addr}): {health} - {role}")

    def start(self):
        print("Discovering Raft cluster nodes...")
        if not self.discover_cluster_configuration():
            print("Unable to discover any nodes. Exiting.")
            return
            
        print(f"Initial discovery complete. Found {len(self.nodes)} nodes.")
        print(f"Leader address: {self.leader_raft_addr}")
        
        try:
            while True:
                self.refresh_status()
                self.display()
                time.sleep(self.refresh_rate)
        except KeyboardInterrupt:
            print("\nMonitoring stopped by user. Exiting...")

def main():
    parser = argparse.ArgumentParser(description='Monitor a Raft cluster')
    parser.add_argument('--refresh', type=int, default=5, help='Refresh rate in seconds (default: 5)')
    parser.add_argument('--pretty', type=bool, default=True, help='Use pretty tabular output (default: True)')
    args = parser.parse_args()

    monitor = ClusterMonitor(refresh_rate=args.refresh, pretty_output=args.pretty)
    monitor.start()

if __name__ == "__main__":
    main()
