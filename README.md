# Start the first node (bootstrap node, defaults to leader)
```go run . --id node1 --raftPort 12000 --httpPort 8080 --dataDir ./data1 --bootstrap```

# Start the second node and join the cluster
```go run . --id node2 --raftPort 12001 --httpPort 8081 --dataDir ./data2```

```curl -X POST -d "{\"id\":\"node2\", \"address\":\"127.0.0.1:12001\"}" http://localhost:8080/join```

# Start the third node and join the cluster
```go run . --id node3 --raftPort 12002 --httpPort 8082 --dataDir ./data3```

```curl -X POST -d "{\"id\":\"node3\", \"address\":\"127.0.0.1:12002\"}" http://localhost:8080/join```


# Add Printers
curl -X POST -d "{\"id\":\"printer1\",\"company\":\"Creality\",\"model\":\"Ender 3 Pro\"}" http://localhost:8080/printers -H "Content-Type: application/json"


# Add Filaments
curl -X POST -d "{\"id\":\"filament1\",\"type\":\"PLA\",\"color\":\"Red\",\"total_weight_in_grams\":1000,\"remaining_weight_in_grams\":950}" http://localhost:8080/filaments -H "Content-Type: application/json"


# Add PrintJobs
curl -X POST -d "{\"printer_id\":\"printer1\",\"filament_id\":\"filament1\",\"filepath\":\"/models/benchy.stl\",\"print_weight_in_grams\":15}" http://localhost:8080/print_jobs -H "Content-Type: application/json"

