# NEXRAD AWS Notifier

[![Release](https://github.com/USA-RedDragon/nexrad-aws-notifier/actions/workflows/release.yaml/badge.svg)](https://github.com/USA-RedDragon/nexrad-aws-notifier/actions/workflows/release.yaml) [![License](https://badgen.net/github/license/USA-RedDragon/nexrad-aws-notifier)](https://github.com/USA-RedDragon/nexrad-aws-notifier/blob/master/LICENSE) [![go.mod version](https://img.shields.io/github/go-mod/go-version/USA-RedDragon/nexrad-aws-notifier.svg)](https://github.com/USA-RedDragon/nexrad-aws-notifier) [![coverage](https://raw.githubusercontent.com/USA-RedDragon/nexrad-aws-notifier/main/.github/badges/coverage.svg)](https://github.com/USA-RedDragon/nexrad-aws-notifier/actions)

This is a simple Go service that subscribes to the AWS SNS topic for NEXRAD radar data and forwards the notification via websocket to any connected clients.

## Configuration

To run this service, you will need to have a valid AWS account and have the necessary permissions to subscribe to the NEXRAD SNS topic. The service uses the AWS SDK for Go, so it will use the default credentials chain to authenticate with AWS, which includes environment variables, shared credentials file, and IAM roles for Amazon EC2. This service does not support credentials in the configuration file.

The service is configured with command line flags, environment variables, or a YAML file, in that order of precedence. The file is `config.yaml` in the working directory if it exists, or the path given with `--config`/`-c`, which must exist. [`config.example.yaml`](config.example.yaml) lists every option. List values in environment variables are comma-separated.

<!-- configulator:begin -->

| Key                          | Type           | Default     | Environment                  | Flag                           | Description                                                                                                                                                                                |
|------------------------------|----------------|-------------|------------------------------|--------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `http.ipv4_host`             | string         | `0.0.0.0`   | `HTTP_IPV4_HOST`             | `--http.ipv4_host`             | The IPv4 address to bind the HTTP server to. 0.0.0.0 is all interfaces                                                                                                                     |
| `http.ipv6_host`             | string         | `::`        | `HTTP_IPV6_HOST`             | `--http.ipv6_host`             | The IPv6 address to bind the HTTP server to. :: is all interfaces                                                                                                                          |
| `http.port`                  | integer        | `8080`      | `HTTP_PORT`                  | `--http.port`                  | The port to bind the HTTP server to. IPv4 and IPv6 share it                                                                                                                                |
| `http.trusted_proxies`       | list of string |             | `HTTP_TRUSTED_PROXIES`       | `--http.trusted_proxies`       | IP addresses or CIDR ranges of reverse proxies trusted to set the X-Forwarded-For header                                                                                                   |
| `http.cors_hosts`            | list of string |             | `HTTP_CORS_HOSTS`            | `--http.cors_hosts`            | Origins allowed to open a websocket. An entry may be a bare host (example.com, matching any port), a host:port (example.com:8080) or a full URL (https://example.com). * allows any origin |
| `http.tracing.enabled`       | boolean        |             | `HTTP_TRACING_ENABLED`       | `--http.tracing.enabled`       | Enable OpenTelemetry tracing                                                                                                                                                               |
| `http.tracing.otlp_endpoint` | string         |             | `HTTP_TRACING_OTLP_ENDPOINT` | `--http.tracing.otlp_endpoint` | The OpenTelemetry collector OTLP gRPC endpoint as host:port. Required when tracing is enabled                                                                                              |
| `http.pprof.enabled`         | boolean        |             | `HTTP_PPROF_ENABLED`         | `--http.pprof.enabled`         | Enable Go pprof                                                                                                                                                                            |
| `http.metrics.enabled`       | boolean        |             | `HTTP_METRICS_ENABLED`       | `--http.metrics.enabled`       | Enable the Prometheus metrics server                                                                                                                                                       |
| `http.metrics.ipv4_host`     | string         | `127.0.0.1` | `HTTP_METRICS_IPV4_HOST`     | `--http.metrics.ipv4_host`     | The IPv4 address to bind the Prometheus metrics server to. 127.0.0.1 is localhost                                                                                                          |
| `http.metrics.ipv6_host`     | string         | `::1`       | `HTTP_METRICS_IPV6_HOST`     | `--http.metrics.ipv6_host`     | The IPv6 address to bind the Prometheus metrics server to. ::1 is localhost                                                                                                                |
| `http.metrics.port`          | integer        | `8081`      | `HTTP_METRICS_PORT`          | `--http.metrics.port`          | The port to bind the Prometheus metrics server to. IPv4 and IPv6 share it                                                                                                                  |

<!-- configulator:end -->

## Routes

### GET `/ws/events/:type/:station`

This route is used to subscribe to radar data for a specific station. The `:type` parameter is the type of radar data to subscribe to and the `:station` parameter is the station ID to subscribe to i.e. `KTLX`.

The `:type` parameter can be one of two values, `nexrad-chunk` or `nexrad-archive`), where `chunk` is the real-time radar data and `archive` is when new full scans are complete.

The `:station` parameter must be a NEXRAD site ID: a WSR-88D such as `KTLX`, `PHKI` or `TJUA`, or a TDWR such as `TBOS`. It _should_ be capitalized, but the service will uppercase it if it is not. The accepted IDs are listed in [`internal/nexrad/stations.go`](internal/nexrad/stations.go). An unknown type or station gets a `400` naming the problem, before the websocket is opened.

Each client IP may hold at most 10 websockets open at once, one per type and station. Past that, further connections get a `429` until one closes.

The service narrows its SNS subscriptions to the stations in use. When more are in use than an SNS filter policy can name, 36 for archives and 150 for chunks, the filter is widened to station prefixes such as `KT`, so every station in use is still delivered and the rest are discarded inside the service.

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
  "station": "KJAX",
  "volume": "415",
  "chunk": "25",
  "chunkType": "I",
  "l2Version": "V06",
  "name": "20240418-033635-025-I",
  "path": "KJAX/415/20240418-033635-025-I"
}
```

`path` is the S3 object key within the `unidata-nexrad-level2-chunks` bucket and `name` is its final segment. The datetime in the key is the volume start time, which is not derivable from the other fields, so `path` is taken directly from the SNS notification rather than reconstructed.

### GET `/health`

This route is used to check the health of the service. It will return a `200` with the text "OK" if the service is running.
