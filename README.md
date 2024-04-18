# NEXRAD AWS Notifier

This is a simple Go service that subscribes to the AWS SNS topic for NEXRAD radar data and forwards the notification via websocket to any connected clients.

## Configuration

To run this service, you will need to have a valid AWS account and have the necessary permissions to subscribe to the NEXRAD SNS topic. The service uses the AWS SDK for Go, so it will use the default credentials chain to authenticate with AWS, which includes environment variables, shared credentials file, and IAM roles for Amazon EC2. This service does not support credentials in the configuration file.

The service is configured via environment variables, a configuration YAML file, or command line flags. The [`config.example.yaml`](config.example.yaml) file shows the available configuration options. The command line flags match the schema of the YAML file, i.e. `--http.cors_hosts='0.0.0.0'` would equate to `http.cors_hosts: ["0.0.0.0"]`. Environment variables are in the same format, however they are uppercase and replace hyphens with underscores and dots with double underscores, i.e. `HTTP__CORS_HOSTS="0.0.0.0"`.

## Routes

### GET `/ws/events/:type/:station`

This route is used to subscribe to radar data for a specific station. The `:type` parameter is the type of radar data to subscribe to and the `:station` parameter is the station ID to subscribe to i.e. `KTLX`.

The `:type` parameter can be one of two values, `nexrad-chunk` or `nexrad-archive`), where `chunk` is the real-time radar data and `archive` is when new full scans are complete.

The `:station` parameter _should_ be capitalized, but the service will uppercase it if it is not.

The events emitted by the websocket for `archive` data are JSON objects with the following structure:

```json
{
  "station": "TBOS",
  "path": "2024/04/18/TBOS/TBOS20240418_033635_V08"
}
```

The events emitted by the websocket for `chunk` data are JSON objects with the following structure:

```json
{
  "station": "KTLX",
  "volume": "",
  "chunk": "",
  "chunkType": "",
  "l2Version": ""
}
```

### GET `/health`

This route is used to check the health of the service. It will return a `200` with the text "OK" if the service is running.
