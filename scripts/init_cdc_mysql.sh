#!/bin/bash

# Configuration
CONNECT_HOST="localhost"
CONNECT_PORT="8083"
CONNECTOR_NAME="mysql-shop-connector"

echo "Registering Debezium MySQL Connector at http://$CONNECT_HOST:$CONNECT_PORT..."

# Register Connector
response=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Accept:application/json" -H "Content-Type:application/json" http://$CONNECT_HOST:$CONNECT_PORT/connectors/ -d '{
  "name": "'"$CONNECTOR_NAME"'",
  "config": {
    "connector.class": "io.debezium.connector.mysql.MySqlConnector",
    "tasks.max": "1",
    "database.hostname": "common-mysql",
    "database.port": "3306",
    "database.user": "root",
    "database.password": "root",
    "database.server.id": "184054",
    "topic.prefix": "XXX",
    "database.include.list": "database",
    "schema.history.internal.kafka.bootstrap.servers": "common-kafka:9092",
    "schema.history.internal.kafka.topic": "schemahistory.database",
    "table.include.list": "database.table",
    "key.converter": "org.apache.kafka.connect.json.JsonConverter",
    "key.converter.schemas.enable": "false",
    "value.converter": "org.apache.kafka.connect.json.JsonConverter",
    "value.converter.schemas.enable": "false"
  }
}')

if [ "$response" -eq 201 ]; then
  echo "Connector registered successfully!"
elif [ "$response" -eq 409 ]; then
  echo "Connector already exists."
else
  echo "Failed to register connector. HTTP Status: $response"
  echo "Configuring failed? Check if Debezium is running: docker ps | grep debezium"
  exit 1
fi
