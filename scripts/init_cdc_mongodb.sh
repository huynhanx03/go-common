#!/bin/bash

# Configuration
CONNECT_HOST="localhost"
CONNECT_PORT="8083"
CONNECTOR_NAME="mongodb-connector"

echo "Registering Debezium Connector at http://$CONNECT_HOST:$CONNECT_PORT..."

# Register Connector
response=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Accept:application/json" -H "Content-Type:application/json" http://$CONNECT_HOST:$CONNECT_PORT/connectors/ -d '{
  "name": "'"$CONNECTOR_NAME"'",
  "config": {
    "connector.class": "io.debezium.connector.mongodb.MongoDbConnector",
    "mongodb.connection.string": "mongodb://common-mongodb:27017/?replicaSet=rs0",
    "topic.prefix": "XXX",
    "collection.include.list": "database.table",
    "key.converter": "org.apache.kafka.connect.storage.StringConverter",
    "value.converter": "org.apache.kafka.connect.json.JsonConverter",
    "value.converter.schemas.enable": "false",
    "transforms": "unwrapKey",
    "transforms.unwrapKey.type": "org.apache.kafka.connect.transforms.ExtractField$Key",
    "transforms.unwrapKey.field": "id"
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
