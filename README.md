# Proxmox Backup Server Exporter

[![license](https://img.shields.io/github/license/natrontech/pbs-exporter)](https://github.com/natrontech/pbs-exporter/blob/main/LICENSE)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/natrontech/pbs-exporter/badge)](https://securityscorecards.dev/viewer/?uri=github.com/natrontech/pbs-exporter)
[![release](https://img.shields.io/github/v/release/natrontech/pbs-exporter)](https://github.com/natrontech/pbs-exporter/releases)
[![go-version](https://img.shields.io/github/go-mod/go-version/natrontech/pbs-exporter)](https://github.com/natrontech/pbs-exporter/blob/main/go.mod)
[![Go Report Card](https://goreportcard.com/badge/github.com/natrontech/pbs-exporter)](https://goreportcard.com/report/github.com/natrontech/pbs-exporter)
[![SLSA 3](https://slsa.dev/images/gh-badge-level3.svg)](https://slsa.dev)

---

Export [Proxmox Backup Server](https://www.proxmox.com/en/proxmox-backup-server/overview) statistics to [Prometheus](https://prometheus.io/).

Metrics are retrieved using the [Proxmox Backup Server API](https://pbs.proxmox.com/docs/api-viewer/index.html).

## Exported Metrics

Each section below lists the permissions the API token needs for its metrics. The simplest setup is to assign the built-in `Audit` role on `/` (`Sys.Audit` and `Datastore.Audit`) and the `RemoteAudit` role on `/remote` (`Remote.Audit`, only needed for sync jobs) to the token:

```bash
proxmox-backup-manager acl update / Audit --auth-id 'root@pam!pbs-exporter'
proxmox-backup-manager acl update /remote RemoteAudit --auth-id 'root@pam!pbs-exporter'
```

If the token uses privilege separation, the permissions have to be granted to the token itself (the effective privileges are the intersection of the user's and the token's).

Missing permissions never stop the exporter or fail the scrape:

- If PBS denies a request (`403`), only the affected metrics are skipped and `pbs_up` stays `1`. A warning naming the missing permission is logged once per target and API path, e.g. `WARN: Missing permission Sys.Audit on /system/status for /api2/json/nodes/localhost/status on endpoint https://pbs:8007, skipping host metrics`. Repeated occurrences are only logged with loglevel `debug`.
- Datastores, jobs and tasks the token is not allowed to see are silently left out of the API responses by PBS, so their metrics are missing without any warning.
- Only an invalid token (`401`), an unreachable server or other errors fail the scrape with `pbs_up 0`, as before.

### General, datastore usage, snapshot and host metrics

| Metric                                      | Meaning                                                               | Labels                                                                       |
| ------------------------------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| pbs_up                                      | Was the last query of Proxmox Backup Server successful?               |                                                                              |
| pbs_version                                 | Version of Proxmox Backup Server                                      | `version`, `repoid`, `release`                                               |
| pbs_available                               | The available bytes of the underlying storage.                        | `datastore`                                                                  |
| pbs_size                                    | The size of the underlying storage in bytes.                          | `datastore`                                                                  |
| pbs_used                                    | The used bytes of the underlying storage.                             | `datastore`                                                                  |
| pbs_snapshot_count                          | The total number of backups.                                          | `datastore`, `namespace`                                                     |
| pbs_snapshot_vm_count                       | The total number of backups per VM.                                   | `datastore`, `namespace`, `vm_id`, `vm_name`                                 |
| pbs_snapshot_vm_last_timestamp              | The timestamp of the last backup of a VM.                             | `datastore`, `namespace`, `vm_id`, `vm_name`                                 |
| pbs_snapshot_vm_last_verify                 | The verify status of the last backup of a VM.                         | `datastore`, `namespace`, `vm_id`, `vm_name`                                 |
| pbs_host_subscription_due_timestamp_seconds | The subscription due timestamp of the host in seconds.                | `productname`                                                                |
| pbs_host_subscription_info                  | The subscription info of the host.                                    | `productname`, `status`                                                      |
| pbs_host_subscription_status                | Indicates if the subscription is in the state indicated by the label. | `status` = (`active`\|`expired`\|`invalid`\|`new`\|`notfound`\|`superseded`) |
| pbs_host_cpu_usage                          | The CPU usage of the host.                                            |                                                                              |
| pbs_host_memory_free                        | The free memory of the host.                                          |                                                                              |
| pbs_host_memory_total                       | The total memory of the host.                                         |                                                                              |
| pbs_host_memory_used                        | The used memory of the host.                                          |                                                                              |
| pbs_host_swap_free                          | The free swap of the host.                                            |                                                                              |
| pbs_host_swap_total                         | The total swap of the host.                                           |                                                                              |
| pbs_host_swap_used                          | The used swap of the host.                                            |                                                                              |
| pbs_host_disk_available                     | The available disk of the local root disk in bytes.                   |                                                                              |
| pbs_host_disk_total                         | The total disk of the local root disk in bytes.                       |                                                                              |
| pbs_host_disk_used                          | The used disk of the local root disk in bytes.                        |                                                                              |
| pbs_host_uptime                             | The uptime of the host.                                               |                                                                              |
| pbs_host_io_wait                            | The io wait of the host.                                              |                                                                              |
| pbs_host_load1                              | The load for 1 minute of the host.                                    |                                                                              |
| pbs_host_load5                              | The load for 5 minutes of the host.                                   |                                                                              |
| pbs_host_load15                             | The load 15 minutes of the host.                                      |                                                                              |

**Required permissions:**

- `pbs_version` and `pbs_host_subscription_*`: any authenticated token.
- `pbs_available`, `pbs_size` and `pbs_used`: `Datastore.Audit` on `/datastore/{store}`. Datastores without it are not listed.
- `pbs_snapshot_*`: `Datastore.Audit` on `/datastore/{store}`, or on `/datastore/{store}/{namespace}` for a single namespace.
- `pbs_host_*` (except `pbs_host_subscription_*`): `Sys.Audit` on `/system/status`. Without it, these metrics are skipped and a warning is logged.

### Datastore metrics

| Metric                                         | Meaning                                                                         | Labels                                                             |
| ---------------------------------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| pbs_datastore_info                             | The info of a datastore (always `1`).                                           | `datastore`, `backend_type`, `mount_status`, `maintenance_mode`    |
| pbs_datastore_estimated_full_timestamp_seconds | The estimated timestamp (unix seconds) when the datastore will be full.         | `datastore`                                                        |
| pbs_datastore_index_data_bytes                 | The bytes referenced by all indexes (before deduplication), as of the last GC.  | `datastore`                                                        |
| pbs_datastore_disk_bytes                       | The bytes used by chunks on disk (after deduplication), as of the last GC.      | `datastore`                                                        |
| pbs_datastore_disk_chunks                      | The number of chunks on disk, as of the last GC.                                | `datastore`                                                        |
| pbs_datastore_index_file_count                 | The number of index files, as of the last GC.                                   | `datastore`                                                        |

`maintenance_mode` is `none` if the datastore is not in maintenance mode, otherwise the maintenance type (e.g. `offline`, `read-only`, `unmount`). The deduplication factor can be calculated with `pbs_datastore_index_data_bytes / pbs_datastore_disk_bytes`.

**Required permissions:** `Datastore.Audit` on `/datastore/{store}`. Datastores without it are not listed, so they get no `pbs_datastore_*` series.

### Task metrics

| Metric                                   | Meaning                                                   | Labels                       |
| ---------------------------------------- | --------------------------------------------------------- | ---------------------------- |
| pbs_task_running_start_timestamp_seconds | The start timestamp (unix seconds) of a currently running task. | `worker_type`, `worker_id` |

One series is exported per running task, e.g. `worker_type="garbage_collection"`, `worker_id="MAIN-DATASTORE"`. If several running tasks have the same type and id, the earliest start time is exported. The runtime of a task is `time() - pbs_task_running_start_timestamp_seconds`, so long running tasks can be alerted on with a threshold per `worker_type`.

**Required permissions:** `Sys.Audit` on `/system/tasks`. :warning: Without it, PBS doesn't deny the request but only lists the token user's own tasks. Scheduled GC, prune, verify and sync jobs (running as `root@pam`) and backups from other users are then missing without any error.

### Garbage collection metrics

| Metric                                | Meaning                                                                          | Labels                                                   |
| ------------------------------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------- |
| pbs_gc_last_run_status                | Indicates if the last GC run is in the state indicated by the label.             | `datastore`, `status` = (`ok`\|`warning`\|`error`)       |
| pbs_gc_last_run_end_timestamp_seconds | The end timestamp (unix seconds) of the last GC run.                             | `datastore`                                              |
| pbs_gc_last_run_duration_seconds      | The duration in seconds of the last GC run.                                      | `datastore`                                              |
| pbs_gc_next_run_timestamp_seconds     | The timestamp (unix seconds) of the next scheduled GC run.                       | `datastore`                                              |
| pbs_gc_running                        | Is a GC currently running.                                                       | `datastore`                                              |
| pbs_gc_last_run_start_timestamp_seconds | The start timestamp (unix seconds) of the last finished GC run.           | `datastore`                                              |
| pbs_gc_removed_bytes                  | The bytes removed by the last GC run.                                            | `datastore`                                              |
| pbs_gc_removed_chunks                 | The chunks removed by the last GC run.                                           | `datastore`                                              |
| pbs_gc_pending_bytes                  | The bytes pending removal after the last GC run.                                 | `datastore`                                              |
| pbs_gc_pending_chunks                 | The chunks pending removal after the last GC run.                                | `datastore`                                              |
| pbs_gc_removed_bad_chunks             | The bad chunks removed by the last GC run.                                       | `datastore`                                              |
| pbs_gc_still_bad_chunks               | The bad chunks still present after the last GC run.                              | `datastore`                                              |

**Required permissions:** `Datastore.Audit` (or `Datastore.Modify`) on `/datastore/{store}`. GC of datastores without it is not listed.

### Sync, prune and verify job metrics

The following metrics exist for each job type, where `<job>` is `sync_job`, `prune_job` or `verify_job`.

| Metric                                         | Meaning                                                                  | Labels                                                     |
| ---------------------------------------------- | ------------------------------------------------------------------------ | ---------------------------------------------------------- |
| pbs\_\<job\>\_info                             | The info of a job (always `1`).                                          | `datastore`, `job_id`, plus the job specific labels below  |
| pbs\_\<job\>\_last_run_status                  | Indicates if the last run of a job is in the state indicated by the label. | `datastore`, `job_id`, `status` = (`ok`\|`warning`\|`error`) |
| pbs\_\<job\>\_last_run_start_timestamp_seconds | The start timestamp (unix seconds) of the last run of a job.             | `datastore`, `job_id`                                      |
| pbs\_\<job\>\_last_run_end_timestamp_seconds   | The end timestamp (unix seconds) of the last run of a job.               | `datastore`, `job_id`                                      |
| pbs\_\<job\>\_last_run_duration_seconds        | The duration in seconds of the last run of a job.                        | `datastore`, `job_id`                                      |
| pbs\_\<job\>\_next_run_timestamp_seconds       | The timestamp (unix seconds) of the next scheduled run of a job.         | `datastore`, `job_id`                                      |
| pbs\_\<job\>\_running                          | Is the job currently running.                                            | `datastore`, `job_id`                                      |

Job specific labels of the `_info` metric: `pbs_sync_job_info` has `remote`, `remote_store` and `direction` (`pull`\|`push`), `pbs_prune_job_info` has `namespace`.

**Required permissions:** jobs the token may not see are silently left out of the list, so a missing permission shows up as missing series, not as an error.

| Job type | Permissions                                                                                         |
| -------- | --------------------------------------------------------------------------------------------------- |
| sync     | `Datastore.Audit` on the local datastore (`/datastore/{store}`) **and** `Remote.Audit` on the remote (`/remote/{remote}`) |
| prune    | `Datastore.Audit` (or `Datastore.Modify`) on `/datastore/{store}`                                    |
| verify   | `Datastore.Audit` (or `Datastore.Verify`) on `/datastore/{store}`                                    |

:warning: `Remote.Audit` is easy to miss: neither the `Audit` nor the `DatastoreAudit` role includes it, so sync jobs stay hidden until it is granted, e.g. with the `RemoteAudit` role on `/remote`.

The last run metrics are only exported once a job (or GC) has finished at least once. While a job is running, PBS doesn't report the result of the previous run, so only `_running` and `_last_run_start_timestamp_seconds` are exported until it finishes. The same applies to GC: while it is running, `pbs_gc_last_run_status`, `pbs_gc_last_run_end_timestamp_seconds` and `pbs_gc_last_run_duration_seconds` are missing, while the GC statistics and `pbs_gc_last_run_start_timestamp_seconds` keep referring to the last finished GC.

## Flags / Environment Variables

```bash
$ ./pbs-exporter -help
```

You can use the following flags to configure the exporter. All flags can also be set using environment variables. Environment variables take precedence over flags.

| Flag                 | Environment Variable | Description                                          | Default                                                |
| -------------------- | -------------------- | ---------------------------------------------------- | ------------------------------------------------------ |
| `pbs.loglevel`       | `PBS_LOGLEVEL`       | Log level (debug, info)                              | `info`                                                 |
| `pbs.api.token`      | `PBS_API_TOKEN`      | API token to use for authentication                  |                                                        |
| `pbs.api.token.name` | `PBS_API_TOKEN_NAME` | Name of the API token to use for authentication      | `pbs-exporter`                                         |
| `pbs.endpoint`       | `PBS_ENDPOINT`       | Address of the Proxmox Backup Server                 | `http://localhost:8007` (if no parameter `target` set) |
| `pbs.username`       | `PBS_USERNAME`       | Username to use for authentication                   | `root@pam`                                             |
| `pbs.timeout`        | `PBS_TIMEOUT`        | Timeout for requests to Proxmox Backup Server        | `5s`                                                   |
| `pbs.insecure`       | `PBS_INSECURE`       | Disable TLS certificate verification                 | `false`                                                |
| `pbs.metrics-path`   | `PBS_METRICS_PATH`   | Path under which to expose metrics                   | `/metrics`                                             |
| `pbs.listen-address` | `PBS_LISTEN_ADDRESS` | Address to listen on for web interface and telemetry | `:10019`                                               |

### Running on PBS (systemd)
The Prometheus-pbs-exporter can also simply be installed on a Proxmox Backup Server instead of spawning an additional Docker container.

```
# Download prometheus-pbs-exporter
mkdir /opt/pbs-exporter
cd /opt/pbs-exporter
wget https://github.com/natrontech/pbs-exporter/releases/download/v0.6.4/pbs-exporter_v0.6.4_linux_amd64.tar.gz
tar xfvz pbs-exporter_v0.6.4_linux_amd64.tar.gz
cp pbs-exporter-linux-amd64 /bin/

# Create a dedicated user for running the prometheus-pbs-exporter
useradd -m pbs-exporter -s /sbin/nologin

# Download systemd unit file
cd /etc/systemd/system
wget https://raw.githubusercontent.com/natrontech/pbs-exporter/refs/heads/main/systemd/prometheus-pbs-exporter.service
systemd daemon-reload

# Create prometheus-pbs-exporter environment file (req. minimum the token)
vi /etc/pbs-exporter.env
# Add the token content and other options if required:
TOKEN=beef-1337-cafe-beef-cafe-1337

# Enable and start the service
systemctl enable prometheus-pbs-exporter.service
systemctl start prometheus-pbs-exporter.service
```

### Docker secrets

If you are using [Docker secrets](https://docs.docker.com/engine/swarm/secrets/), you can use the following environment variables to set the path to the secrets:

| Environment Variable      | Description                     |
| ------------------------- | ------------------------------- |
| `PBS_API_TOKEN_FILE`      | Path to the API token file      |
| `PBS_API_TOKEN_NAME_FILE` | Path to the API token name file |
| `PBS_USERNAME_FILE`       | Path to the username file       |

See an example of how to use Docker secrets with Docker Compose in the [docker-compose-secrets.yaml](docker-compose-secrets.yaml) file.

The variables `PBS_API_TOKEN`, `PBS_API_TOKEN_NAME`, and `PBS_USERNAME` take precedence over the secret files.

## Multiple Proxmox Backup Servers

If you want to monitor multiple Proxmox Backup Servers, you can use the `targets` parameter in the query string. Instead of setting the `pbs.endpoint` flag (or `PBS_ENDPOINT` env), you can use the `target` parameter in the query string to specify the Proxmox Backup Server to monitor. You would then use following URL to scrape metrics: `http://localhost:10019/metrics?target=http://10.10.10.10:8007`.

This is useful if you are using Prometheus and want to monitor multiple Proxmox Backup Servers with one "pbs-exporter" instance.
You find examples for Prometheus static configuration in the [prometheus/static-config](prometheus/static-config) directory.

:warning: **Important**: if `pbs.endpoint` or `PBS_ENDPOINT` is set, the `target` parameter is ignored.

## Node metrics

According to the [api documentation](https://pbs.proxmox.com/docs/api-viewer/index.html#/nodes/{node}), we have to provide a node name (won't work with the node ip), but it seems to work with any name, so we just use "localhost" for the request. This setup is tested with one proxmox backup server host.

The node status request (`pbs_host_*` metrics) requires `Sys.Audit` on `/system/status`, see [Exported Metrics](#exported-metrics).

## Supported versions

We have tested the exporter with Proxmox Backup Server version **3.X** (see [Proxmox Backup Server Roadmap](https://pbs.proxmox.com/wiki/index.php/Roadmap)). If you have already tested the exporter with a newer version, or have encountered problems, please let us know.

## Release

Each release of the application includes Go-binary archives, checksums file, SBOMs and container images. 

The release workflow creates provenance for its builds using the [SLSA standard](https://slsa.dev), which conforms to the [Level 3 specification](https://slsa.dev/spec/v1.2/build-track-basics#build-l3). Each artifact can be verified using the `slsa-verifier` or `cosign` tool (see [Release verification](SECURITY.md#release-verification)).
