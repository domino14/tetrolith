#!/bin/bash

# Check if SECRET_KEY environment variable is set
if [ -z "$SECRET_KEY" ]; then
  echo "Error: SECRET_KEY environment variable is not set"
  exit 1
fi


# Function to run the Go script and capture the output JWT
run_go_script() {
  local connuser=$1
  local jwt_output

  # Run the Go script with the parameter
  jwt_output=$(SECRET_KEY=$SECRET_KEY go run ../cmd/jwt/main.go "$connuser")

  # Check if the Go script ran successfully
  if [ $? -ne 0 ]; then
    echo "Error: Go script failed to run"
    exit 1
  fi

  echo "$jwt_output"
}

# Function to open and maintain the WebSocket connection
open_websocket() {
  local jwt=$1
  local websocket_url="ws://localhost:8087/ws?token=${jwt}"

  # Use websocat to open the WebSocket connection and keep it open
  websocat "$websocket_url"
}

# Main script execution
main() {
  if [ $# -lt 1 ]; then
    echo "Usage: $0 <param>"
    exit 1
  fi

  local param=$1
  local jwt_output

  # Run the Go script and capture the JWT output
  jwt_output=$(run_go_script "$param")

  # Open and maintain the WebSocket connection
  open_websocket "$jwt_output"
}

# Run the main function with all script arguments
main "$@"