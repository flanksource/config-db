#!/bin/bash

if [[ "$1" == "--loki" || "$1" == "-l" ]]; then
  echo "Seeding logs to Loki..."
  curl -X POST http://localhost:3100/loki/api/v1/push \
    -H "Content-Type: application/json" \
    -d '{
      "streams": [
        {
          "stream": {
            "job": "app"
          },
          "values": [
            ["'$(date +%s%N)'", "Configuration reloaded: database.max_connections changed from 100 to 200"],
            ["'$(date +%s%N)'", "Configuration reloaded: server.timeout changed from 30s to 60s"],
            ["'$(date +%s%N)'", "Configuration reloaded: cache.size changed from 1GB to 2GB"]
          ]
        }
      ]
    }'
else
  echo "Usage: $0 --loki"
  echo "  --loki, -l    Seed logs to Loki"
  exit 1
fi
