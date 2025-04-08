#!/bin/bash

dirs=("data1" "data2" "data3")

echo "Starting directory cleanup..."

for dir in "${dirs[@]}"; do
  if [ -d "$dir" ]; then
    echo "Cleaning contents of $dir..."
    rm -rf "$dir"/*
    rm -rf "$dir"/.[!.]* "$dir"/..?* 2>/dev/null
  else
    echo "$dir does not exist, skipping..."
  fi
done

echo "Directory cleanup complete."

echo "Running Go commands..."

go mod tidy
go get github.com/hashicorp/raft
go get github.com/hashicorp/raft-boltdb

echo "Go dependencies updated."

