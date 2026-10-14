package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const promNamespace = "pbs"
const versionApi = "/api2/json/version"
const datastoreUsageApi = "/api2/json/status/datastore-usage"
const datastoreApi = "/api2/json/admin/datastore"
const nodeApi = "/api2/json/nodes"
const gcApi = "/api2/json/admin/gc"
const syncApi = "/api2/json/admin/sync"
const pruneApi = "/api2/json/admin/prune"
const verifyApi = "/api2/json/admin/verify"

// These variables are set in build step
var Version = "v0.0.0-dev.0"
var Commit = "none"
var BuildTime = "unknown"

var (
	tr = &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	client = &http.Client{
		Transport: tr,
	}

	// Flags
	endpoint = flag.String("pbs.endpoint", "",
		"Proxmox Backup Server endpoint")
	username = flag.String("pbs.username", "root@pam",
		"Proxmox Backup Server username")
	apitoken = flag.String("pbs.api.token", "",
		"Proxmox Backup Server API token")
	apitokenname = flag.String("pbs.api.token.name", "pbs-exporter",
		"Proxmox Backup Server API token name")
	timeout = flag.String("pbs.timeout", "5s",
		"Proxmox Backup Server timeout")
	insecure = flag.String("pbs.insecure", "false",
		"Proxmox Backup Server insecure")
	metricsPath = flag.String("pbs.metrics-path", "/metrics",
		"Path under which to expose metrics")
	listenAddress = flag.String("pbs.listen-address", ":10019",
		"Address on which to expose metrics")
	loglevel = flag.String("pbs.loglevel", "info",
		"Loglevel")
	showVersion = flag.Bool("version", false, "Show version and exit")

	// Metrics
	up = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "up"),
		"Was the last query of PBS successful.",
		nil, nil,
	)
	version = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "version"),
		"Version of the PBS installation.",
		[]string{"version", "repoid", "release"}, nil,
	)
	available = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "available"),
		"The available bytes of the underlying storage.",
		[]string{"datastore"}, nil,
	)
	size = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "size"),
		"The size of the underlying storage in bytes.",
		[]string{"datastore"}, nil,
	)
	used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "used"),
		"The used bytes of the underlying storage.",
		[]string{"datastore"}, nil,
	)
	snapshot_count = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "snapshot_count"),
		"The total number of backups.",
		[]string{"datastore", "namespace"}, nil,
	)
	snapshot_vm_count = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "snapshot_vm_count"),
		"The total number of backups per VM.",
		[]string{"datastore", "namespace", "vm_id", "vm_name"}, nil,
	)
	snapshot_vm_last_timestamp = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "snapshot_vm_last_timestamp"),
		"The timestamp of the last backup of a VM.",
		[]string{"datastore", "namespace", "vm_id", "vm_name"}, nil,
	)
	snapshot_vm_last_verify = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "snapshot_vm_last_verify"),
		"The verify status of the last backup of a VM.",
		[]string{"datastore", "namespace", "vm_id", "vm_name"}, nil,
	)
	subscription_status = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_subscription_status"),
		"The subscription status of the host.",
		[]string{"status"}, nil,
	)
	subscription_info = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_subscription_info"),
		"The subscription info of the host.",
		[]string{"productname", "status"}, nil,
	)
	subscription_due_timestamp_seconds = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_subscription_due_timestamp_seconds"),
		"The subscription next due timestamp (unix seconds) of the host.",
		[]string{"productname"}, nil,
	)
	host_cpu_usage = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_cpu_usage"),
		"The CPU usage of the host.",
		nil, nil,
	)
	host_memory_free = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_memory_free"),
		"The free memory of the host.",
		nil, nil,
	)
	host_memory_total = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_memory_total"),
		"The total memory of the host.",
		nil, nil,
	)
	host_memory_used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_memory_used"),
		"The used memory of the host.",
		nil, nil,
	)
	host_swap_free = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_swap_free"),
		"The free swap of the host.",
		nil, nil,
	)
	host_swap_total = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_swap_total"),
		"The total swap of the host.",
		nil, nil,
	)
	host_swap_used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_swap_used"),
		"The used swap of the host.",
		nil, nil,
	)
	host_disk_available = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_disk_available"),
		"The available disk of the local root disk in bytes.",
		nil, nil,
	)
	host_disk_total = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_disk_total"),
		"The total disk of the local root disk in bytes.",
		nil, nil,
	)
	host_disk_used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_disk_used"),
		"The used disk of the local root disk in bytes.",
		nil, nil,
	)
	host_uptime = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_uptime"),
		"The uptime of the host.",
		nil, nil,
	)
	host_io_wait = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_io_wait"),
		"The io wait of the host.",
		nil, nil,
	)
	host_load1 = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_load1"),
		"The load for 1 minute of the host.",
		nil, nil,
	)
	host_load5 = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_load5"),
		"The load for 5 minutes of the host.",
		nil, nil,
	)
	host_load15 = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_load15"),
		"The load for 15 minutes of the host.",
		nil, nil,
	)
	datastore_info = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "datastore_info"),
		"The info of a datastore.",
		[]string{"datastore", "backend_type", "mount_status", "maintenance_mode"}, nil,
	)
	datastore_estimated_full_timestamp_seconds = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "datastore_estimated_full_timestamp_seconds"),
		"The estimated timestamp (unix seconds) when the datastore will be full.",
		[]string{"datastore"}, nil,
	)
	datastore_index_data_bytes = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "datastore_index_data_bytes"),
		"The bytes referenced by all indexes of the datastore (before deduplication), as of the last GC.",
		[]string{"datastore"}, nil,
	)
	datastore_disk_bytes = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "datastore_disk_bytes"),
		"The bytes used by chunks on disk (after deduplication), as of the last GC.",
		[]string{"datastore"}, nil,
	)
	datastore_disk_chunks = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "datastore_disk_chunks"),
		"The number of chunks on disk, as of the last GC.",
		[]string{"datastore"}, nil,
	)
	datastore_index_file_count = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "datastore_index_file_count"),
		"The number of index files of the datastore, as of the last GC.",
		[]string{"datastore"}, nil,
	)
	gc_last_run_status = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_last_run_status"),
		"Indicates if the last garbage collection run is in the state indicated by the label.",
		[]string{"datastore", "status"}, nil,
	)
	gc_last_run_end_timestamp_seconds = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_last_run_end_timestamp_seconds"),
		"The end timestamp (unix seconds) of the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_last_run_duration_seconds = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_last_run_duration_seconds"),
		"The duration in seconds of the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_next_run_timestamp_seconds = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_next_run_timestamp_seconds"),
		"The timestamp (unix seconds) of the next scheduled garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_running = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_running"),
		"Is a garbage collection currently running.",
		[]string{"datastore"}, nil,
	)
	gc_last_run_start_timestamp_seconds = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_last_run_start_timestamp_seconds"),
		"The start timestamp (unix seconds) of the last finished garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_removed_bytes = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_removed_bytes"),
		"The bytes removed by the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_removed_chunks = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_removed_chunks"),
		"The chunks removed by the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_pending_bytes = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_pending_bytes"),
		"The bytes pending removal after the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_pending_chunks = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_pending_chunks"),
		"The chunks pending removal after the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_removed_bad_chunks = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_removed_bad_chunks"),
		"The bad chunks removed by the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	gc_still_bad_chunks = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "gc_still_bad_chunks"),
		"The bad chunks still present after the last garbage collection run.",
		[]string{"datastore"}, nil,
	)
	sync_job_descs                       = newJobDescs("sync_job", "sync job", []string{"remote", "remote_store", "direction"})
	prune_job_descs                      = newJobDescs("prune_job", "prune job", []string{"namespace"})
	verify_job_descs                     = newJobDescs("verify_job", "verify job", nil)
	task_running_start_timestamp_seconds = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "task_running_start_timestamp_seconds"),
		"The start timestamp (unix seconds) of a currently running task.",
		[]string{"worker_type", "worker_id"}, nil,
	)

	jobStatuses = []string{"ok", "warning", "error"}
)

// jobDescs holds the metric descriptions shared by sync, prune and verify jobs.
type jobDescs struct {
	info             *prometheus.Desc
	lastRunStatus    *prometheus.Desc
	lastRunStartTime *prometheus.Desc
	lastRunEndTime   *prometheus.Desc
	lastRunDuration  *prometheus.Desc
	nextRunTime      *prometheus.Desc
	running          *prometheus.Desc
}

func newJobDescs(kind string, name string, infoLabels []string) jobDescs {
	labels := []string{"datastore", "job_id"}
	return jobDescs{
		info: prometheus.NewDesc(
			prometheus.BuildFQName(promNamespace, "", kind+"_info"),
			"The info of a "+name+".",
			append(append([]string{}, labels...), infoLabels...), nil,
		),
		lastRunStatus: prometheus.NewDesc(
			prometheus.BuildFQName(promNamespace, "", kind+"_last_run_status"),
			"Indicates if the last run of a "+name+" is in the state indicated by the label.",
			append(append([]string{}, labels...), "status"), nil,
		),
		lastRunStartTime: prometheus.NewDesc(
			prometheus.BuildFQName(promNamespace, "", kind+"_last_run_start_timestamp_seconds"),
			"The start timestamp (unix seconds) of the last run of a "+name+".",
			labels, nil,
		),
		lastRunEndTime: prometheus.NewDesc(
			prometheus.BuildFQName(promNamespace, "", kind+"_last_run_end_timestamp_seconds"),
			"The end timestamp (unix seconds) of the last run of a "+name+".",
			labels, nil,
		),
		lastRunDuration: prometheus.NewDesc(
			prometheus.BuildFQName(promNamespace, "", kind+"_last_run_duration_seconds"),
			"The duration in seconds of the last run of a "+name+".",
			labels, nil,
		),
		nextRunTime: prometheus.NewDesc(
			prometheus.BuildFQName(promNamespace, "", kind+"_next_run_timestamp_seconds"),
			"The timestamp (unix seconds) of the next scheduled run of a "+name+".",
			labels, nil,
		),
		running: prometheus.NewDesc(
			prometheus.BuildFQName(promNamespace, "", kind+"_running"),
			"Is the "+name+" currently running.",
			labels, nil,
		),
	}
}

func (d jobDescs) describe(ch chan<- *prometheus.Desc) {
	ch <- d.info
	ch <- d.lastRunStatus
	ch <- d.lastRunStartTime
	ch <- d.lastRunEndTime
	ch <- d.lastRunDuration
	ch <- d.nextRunTime
	ch <- d.running
}

type VersionResponse struct {
	Data struct {
		Release string `json:"release"`
		Repoid  string `json:"repoid"`
		Version string `json:"version"`
	} `json:"data"`
}

type DatastoreResponse struct {
	Data []struct {
		Avail             int64     `json:"avail"`
		Store             string    `json:"store"`
		Total             int64     `json:"total"`
		Used              int64     `json:"used"`
		Namespace         string    `json:"ns"`
		EstimatedFullDate int64     `json:"estimated-full-date"`
		GCStatus          *GCStatus `json:"gc-status"`
	} `json:"data"`
}

type Datastore struct {
	Avail             int64     `json:"avail"`
	Store             string    `json:"store"`
	Total             int64     `json:"total"`
	Used              int64     `json:"used"`
	Namespace         string    `json:"ns"`
	EstimatedFullDate int64     `json:"estimated-full-date"`
	GCStatus          *GCStatus `json:"gc-status"`
}

type GCStatus struct {
	DiskBytes      int64  `json:"disk-bytes"`
	DiskChunks     int64  `json:"disk-chunks"`
	IndexDataBytes int64  `json:"index-data-bytes"`
	IndexFileCount int64  `json:"index-file-count"`
	PendingBytes   int64  `json:"pending-bytes"`
	PendingChunks  int64  `json:"pending-chunks"`
	RemovedBad     int64  `json:"removed-bad"`
	RemovedBytes   int64  `json:"removed-bytes"`
	RemovedChunks  int64  `json:"removed-chunks"`
	StillBad       int64  `json:"still-bad"`
	Upid           string `json:"upid"`
}

type DatastoreListResponse struct {
	Data []struct {
		Store       string `json:"store"`
		BackendType string `json:"backend-type"`
		MountStatus string `json:"mount-status"`
		Maintenance string `json:"maintenance"`
	} `json:"data"`
}

type GCJobResponse struct {
	Data []struct {
		GCStatus
		Store          string `json:"store"`
		LastRunState   string `json:"last-run-state"`
		LastRunEndtime *int64 `json:"last-run-endtime"`
		Duration       *int64 `json:"duration"`
		NextRun        *int64 `json:"next-run"`
	} `json:"data"`
}

// JobStatus is the run state PBS returns for sync, prune and verify jobs.
type JobStatus struct {
	LastRunState   string `json:"last-run-state"`
	LastRunEndtime *int64 `json:"last-run-endtime"`
	LastRunUpid    string `json:"last-run-upid"`
	NextRun        *int64 `json:"next-run"`
}

type SyncJobResponse struct {
	Data []struct {
		JobStatus
		ID            string `json:"id"`
		Store         string `json:"store"`
		Remote        string `json:"remote"`
		RemoteStore   string `json:"remote-store"`
		SyncDirection string `json:"sync-direction"`
	} `json:"data"`
}

type PruneJobResponse struct {
	Data []struct {
		JobStatus
		ID        string `json:"id"`
		Store     string `json:"store"`
		Namespace string `json:"ns"`
	} `json:"data"`
}

type TaskResponse struct {
	Data []struct {
		WorkerType string `json:"worker_type"`
		WorkerID   string `json:"worker_id"`
		StartTime  int64  `json:"starttime"`
	} `json:"data"`
}

type VerifyJobResponse struct {
	Data []struct {
		JobStatus
		ID    string `json:"id"`
		Store string `json:"store"`
	} `json:"data"`
}

type NamespaceResponse struct {
	Data []struct {
		Namespace string `json:"ns"`
	} `json:"data"`
}

type SnapshotResponse struct {
	Data []struct {
		BackupID     string `json:"backup-id"`
		BackupTime   int64  `json:"backup-time"`
		VMName       string `json:"comment"`
		Verification struct {
			State string `json:"state"`
		} `json:"verification"`
	} `json:"data"`
}

type HostResponse struct {
	Data struct {
		CPU float64 `json:"cpu"`
		Mem struct {
			Free  int64 `json:"free"`
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"memory"`
		Swap struct {
			Free  int64 `json:"free"`
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"swap"`
		Disk struct {
			Avail int64 `json:"avail"`
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"root"`
		Load   []float64 `json:"loadavg"`
		Uptime int64     `json:"uptime"`
		Wait   float64   `json:"wait"`
	} `json:"data"`
}

type Exporter struct {
	endpoint            string
	authorizationHeader string
}

func ReadSecretFile(secretfilename string) string {
	// #nosec G703 -- path comes from operator-set *_FILE env vars (docker secrets), not untrusted input
	file, err := os.Open(filepath.Clean(secretfilename))
	// flag to check the file format
	if err != nil {
		log.Fatal(err)
	}
	// Close the file
	defer func() {
		if err = file.Close(); err != nil {
			log.Fatal(err)
		}
	}()
	// Read the first line
	line := bufio.NewScanner(file)
	line.Scan()
	return line.Text()
}

func NewExporter(endpoint string, username string, apitoken string, apitokenname string) *Exporter {
	return &Exporter{
		endpoint:            endpoint,
		authorizationHeader: "PBSAPIToken=" + username + "!" + apitokenname + ":" + apitoken,
	}
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- up
	ch <- version
	ch <- available
	ch <- size
	ch <- used
	ch <- snapshot_count
	ch <- snapshot_vm_count
	ch <- snapshot_vm_last_timestamp
	ch <- snapshot_vm_last_verify
	ch <- subscription_info
	ch <- subscription_status
	ch <- subscription_due_timestamp_seconds
	ch <- host_cpu_usage
	ch <- host_memory_free
	ch <- host_memory_total
	ch <- host_memory_used
	ch <- host_swap_free
	ch <- host_swap_total
	ch <- host_swap_used
	ch <- host_disk_available
	ch <- host_disk_total
	ch <- host_disk_used
	ch <- host_uptime
	ch <- host_io_wait
	ch <- host_load1
	ch <- host_load5
	ch <- host_load15
	ch <- datastore_info
	ch <- datastore_estimated_full_timestamp_seconds
	ch <- datastore_index_data_bytes
	ch <- datastore_disk_bytes
	ch <- datastore_disk_chunks
	ch <- datastore_index_file_count
	ch <- gc_last_run_status
	ch <- gc_last_run_end_timestamp_seconds
	ch <- gc_last_run_duration_seconds
	ch <- gc_next_run_timestamp_seconds
	ch <- gc_running
	ch <- gc_last_run_start_timestamp_seconds
	ch <- gc_removed_bytes
	ch <- gc_removed_chunks
	ch <- gc_pending_bytes
	ch <- gc_pending_chunks
	ch <- gc_removed_bad_chunks
	ch <- gc_still_bad_chunks
	ch <- task_running_start_timestamp_seconds
	sync_job_descs.describe(ch)
	prune_job_descs.describe(ch)
	verify_job_descs.describe(ch)
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	err := e.collectFromAPI(ch)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(
			up, prometheus.GaugeValue, 0,
		)
		log.Println(err)
		return
	}
	ch <- prometheus.MustNewConstMetric(
		up, prometheus.GaugeValue, 1,
	)

}

func (e *Exporter) collectFromAPI(ch chan<- prometheus.Metric) error {

	// get version
	err := e.getVersion(ch)
	if err != nil {
		return err
	}

	// get datastores
	req, err := http.NewRequest("GET", e.endpoint+datastoreUsageApi, nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Request URL: %s", req.URL)
		//		log.Printf("DEBUG: Request Header: %s", vmID)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		log.Printf("Error closing response body: %v", err)
	}
	if err != nil {
		return err
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response DatastoreResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// for each datastore collect metrics
	for _, datastore := range response.Data {
		err := e.getDatastoreMetric(datastore, ch)
		if err != nil {
			return err
		}
	}

	// get node metrics
	err = e.getNodeMetrics(ch)
	if err != nil {
		return err
	}

	// get node subscription metrics
	err = e.getNodeSubscriptionMetrics(ch)
	if err != nil {
		return err
	}

	// get datastore info, gc and job metrics
	// errors are only logged, so a token without access to these endpoints doesn't fail the whole scrape
	collectors := []struct {
		name       string
		path       string
		permission string
		collect    func(chan<- prometheus.Metric) error
	}{
		{"datastore info", datastoreApi, "Datastore.Audit on /datastore", e.getDatastoreInfoMetrics},
		{"garbage collection", gcApi, "Datastore.Audit on /datastore", e.getGCMetrics},
		{"sync job", syncApi, "Datastore.Audit on /datastore and Remote.Audit on /remote", e.getSyncJobMetrics},
		{"prune job", pruneApi, "Datastore.Audit on /datastore", e.getPruneJobMetrics},
		{"verify job", verifyApi, "Datastore.Audit on /datastore", e.getVerifyJobMetrics},
		{"running task", nodeApi + "/localhost/tasks", "Sys.Audit on /system/tasks", e.getRunningTaskMetrics},
	}
	for _, c := range collectors {
		err := c.collect(ch)
		if errors.Is(err, errForbidden) {
			logMissingPermission(e.endpoint, c.path, c.permission, c.name+" metrics")
		} else if err != nil {
			log.Printf("WARN: Unable to collect %s metrics: %s", c.name, strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", ""), "\r", ""))
		}
	}

	return nil
}

// errForbidden is returned when the API token lacks the permissions for a request.
var errForbidden = errors.New("permission denied")

// loggedMissingPermissions remembers which missing permissions were already logged, so they are logged once per target and api path.
var loggedMissingPermissions sync.Map

// logMissingPermission logs that metrics are skipped because of a missing permission.
// It's logged once per target and api path, repeated occurrences only with debug loglevel.
func logMissingPermission(endpoint string, path string, permission string, skipped string) {
	msg := fmt.Sprintf("Missing permission %s for %s on endpoint %s, skipping %s", permission, path, endpoint, skipped)
	msg = strings.ReplaceAll(strings.ReplaceAll(msg, "\n", ""), "\r", "")
	if _, logged := loggedMissingPermissions.LoadOrStore(endpoint+path, true); logged {
		if *loglevel == "debug" {
			log.Printf("DEBUG: %s", msg)
		}
		return
	}
	log.Printf("WARN: %s", msg)
}

// getJSON requests the given api path and unmarshals the json response into out.
func (e *Exporter) getJSON(path string, out any) error {
	req, err := http.NewRequest("GET", e.endpoint+path, nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Request URL: %s", req.URL)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		log.Printf("Error closing response body: %v", err)
	}
	if err != nil {
		return err
	}

	// check if status code is 200
	if resp.StatusCode == http.StatusForbidden {
		return errForbidden
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// parse json
	return json.Unmarshal(body, out)
}

func (e *Exporter) getDatastoreInfoMetrics(ch chan<- prometheus.Metric) error {
	var response DatastoreListResponse
	if err := e.getJSON(datastoreApi, &response); err != nil {
		return err
	}

	for _, datastore := range response.Data {
		ch <- prometheus.MustNewConstMetric(
			datastore_info, prometheus.GaugeValue, 1,
			datastore.Store, datastore.BackendType, datastore.MountStatus, parseMaintenanceType(datastore.Maintenance),
		)
	}

	return nil
}

func (e *Exporter) getGCMetrics(ch chan<- prometheus.Metric) error {
	var response GCJobResponse
	if err := e.getJSON(gcApi, &response); err != nil {
		return err
	}

	for _, gc := range response.Data {
		// same logic as the PBS web UI: while a gc is running, last-run-endtime is missing,
		// while upid and statistics still belong to the last finished gc
		running := 0.0
		if gc.Upid != "" && gc.LastRunEndtime == nil {
			running = 1.0
		}
		ch <- prometheus.MustNewConstMetric(
			gc_running, prometheus.GaugeValue, running, gc.Store,
		)
		if gc.NextRun != nil {
			ch <- prometheus.MustNewConstMetric(
				gc_next_run_timestamp_seconds, prometheus.GaugeValue, float64(*gc.NextRun), gc.Store,
			)
		}

		// the remaining metrics are only available if gc has finished at least once
		if gc.Upid == "" {
			continue
		}
		if startTime, ok := parseUpidStartTime(gc.Upid); ok {
			ch <- prometheus.MustNewConstMetric(
				gc_last_run_start_timestamp_seconds, prometheus.GaugeValue, float64(startTime), gc.Store,
			)
		}
		if gc.LastRunState != "" {
			emitJobStatus(ch, gc_last_run_status, gc.LastRunState, gc.Store)
		}
		if gc.LastRunEndtime != nil {
			ch <- prometheus.MustNewConstMetric(
				gc_last_run_end_timestamp_seconds, prometheus.GaugeValue, float64(*gc.LastRunEndtime), gc.Store,
			)
		}
		if gc.Duration != nil {
			ch <- prometheus.MustNewConstMetric(
				gc_last_run_duration_seconds, prometheus.GaugeValue, float64(*gc.Duration), gc.Store,
			)
		}
		ch <- prometheus.MustNewConstMetric(
			gc_removed_bytes, prometheus.GaugeValue, float64(gc.RemovedBytes), gc.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			gc_removed_chunks, prometheus.GaugeValue, float64(gc.RemovedChunks), gc.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			gc_pending_bytes, prometheus.GaugeValue, float64(gc.PendingBytes), gc.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			gc_pending_chunks, prometheus.GaugeValue, float64(gc.PendingChunks), gc.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			gc_removed_bad_chunks, prometheus.GaugeValue, float64(gc.RemovedBad), gc.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			gc_still_bad_chunks, prometheus.GaugeValue, float64(gc.StillBad), gc.Store,
		)
	}

	return nil
}

func (e *Exporter) getSyncJobMetrics(ch chan<- prometheus.Metric) error {
	var response SyncJobResponse
	if err := e.getJSON(syncApi, &response); err != nil {
		return err
	}

	for _, job := range response.Data {
		direction := job.SyncDirection
		if direction == "" {
			direction = "pull"
		}
		ch <- prometheus.MustNewConstMetric(
			sync_job_descs.info, prometheus.GaugeValue, 1, job.Store, job.ID, job.Remote, job.RemoteStore, direction,
		)
		emitJobMetrics(ch, sync_job_descs, job.JobStatus, job.Store, job.ID)
	}

	return nil
}

func (e *Exporter) getPruneJobMetrics(ch chan<- prometheus.Metric) error {
	var response PruneJobResponse
	if err := e.getJSON(pruneApi, &response); err != nil {
		return err
	}

	for _, job := range response.Data {
		ch <- prometheus.MustNewConstMetric(
			prune_job_descs.info, prometheus.GaugeValue, 1, job.Store, job.ID, job.Namespace,
		)
		emitJobMetrics(ch, prune_job_descs, job.JobStatus, job.Store, job.ID)
	}

	return nil
}

func (e *Exporter) getVerifyJobMetrics(ch chan<- prometheus.Metric) error {
	var response VerifyJobResponse
	if err := e.getJSON(verifyApi, &response); err != nil {
		return err
	}

	for _, job := range response.Data {
		ch <- prometheus.MustNewConstMetric(
			verify_job_descs.info, prometheus.GaugeValue, 1, job.Store, job.ID,
		)
		emitJobMetrics(ch, verify_job_descs, job.JobStatus, job.Store, job.ID)
	}

	return nil
}

func (e *Exporter) getRunningTaskMetrics(ch chan<- prometheus.Metric) error {
	// NOTE: without Sys.Audit on /system/tasks, PBS only lists the tasks of the token's own user
	var response TaskResponse
	if err := e.getJSON(nodeApi+"/localhost/tasks?running=1&limit=0", &response); err != nil {
		return err
	}

	// several tasks can share type and id, keep the earliest start time to catch the longest running one
	startTimes := make(map[[2]string]int64)
	for _, task := range response.Data {
		key := [2]string{task.WorkerType, task.WorkerID}
		if startTime, ok := startTimes[key]; !ok || task.StartTime < startTime {
			startTimes[key] = task.StartTime
		}
	}
	for key, startTime := range startTimes {
		ch <- prometheus.MustNewConstMetric(
			task_running_start_timestamp_seconds, prometheus.GaugeValue, float64(startTime), key[0], key[1],
		)
	}

	return nil
}

// emitJobMetrics sets the run state metrics of a sync, prune or verify job.
func emitJobMetrics(ch chan<- prometheus.Metric, descs jobDescs, js JobStatus, labels ...string) {
	running := 0.0
	if js.LastRunUpid != "" && js.LastRunEndtime == nil {
		running = 1.0
	}
	ch <- prometheus.MustNewConstMetric(
		descs.running, prometheus.GaugeValue, running, labels...,
	)
	if js.NextRun != nil {
		ch <- prometheus.MustNewConstMetric(
			descs.nextRunTime, prometheus.GaugeValue, float64(*js.NextRun), labels...,
		)
	}

	startTime, hasStartTime := parseUpidStartTime(js.LastRunUpid)
	if hasStartTime {
		ch <- prometheus.MustNewConstMetric(
			descs.lastRunStartTime, prometheus.GaugeValue, float64(startTime), labels...,
		)
	}

	// the remaining metrics are only available if the job has finished at least once
	if js.LastRunState == "" {
		return
	}
	emitJobStatus(ch, descs.lastRunStatus, js.LastRunState, labels...)
	if js.LastRunEndtime != nil {
		ch <- prometheus.MustNewConstMetric(
			descs.lastRunEndTime, prometheus.GaugeValue, float64(*js.LastRunEndtime), labels...,
		)
		if hasStartTime {
			ch <- prometheus.MustNewConstMetric(
				descs.lastRunDuration, prometheus.GaugeValue, float64(*js.LastRunEndtime-startTime), labels...,
			)
		}
	}
}

// emitJobStatus emits a metric for each possible job status with 1/0
func emitJobStatus(ch chan<- prometheus.Metric, desc *prometheus.Desc, state string, labels ...string) {
	category := jobStatusCategory(state)
	for _, s := range jobStatuses {
		val := 0.0
		if category == s {
			val = 1.0
		}
		ch <- prometheus.MustNewConstMetric(
			desc, prometheus.GaugeValue, val, append(append([]string{}, labels...), s)...,
		)
	}
}

// jobStatusCategory maps the PBS task state ("OK", "WARNINGS: <n>" or an error message) to ok, warning or error.
func jobStatusCategory(state string) string {
	switch {
	case state == "OK":
		return "ok"
	case strings.HasPrefix(state, "WARNINGS"):
		return "warning"
	default:
		return "error"
	}
}

// parseUpidStartTime returns the start time encoded in a PBS UPID
// (UPID:node:pid:pstart:task_id:starttime:wtype:wid:authid:, starttime in hex).
func parseUpidStartTime(upid string) (int64, bool) {
	parts := strings.Split(upid, ":")
	if len(parts) < 9 || parts[0] != "UPID" {
		return 0, false
	}
	startTime, err := strconv.ParseInt(parts[5], 16, 64)
	if err != nil {
		return 0, false
	}
	return startTime, true
}

// parseMaintenanceType returns the type of a maintenance mode string like "type=offline,message=...".
func parseMaintenanceType(maintenance string) string {
	for _, part := range strings.Split(maintenance, ",") {
		key, value, found := strings.Cut(part, "=")
		if !found {
			// the type is the default key, so "offline" equals "type=offline"
			key, value = "type", key
		}
		if key == "type" && value != "" {
			return value
		}
	}
	return "none"
}

func (e *Exporter) getVersion(ch chan<- prometheus.Metric) error {
	// get version
	req, err := http.NewRequest("GET", e.endpoint+versionApi, nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Request URL: %s", req.URL)
		//		log.Printf("DEBUG: Request Header: %s", vmID)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		log.Printf("Error closing response body: %v", err)
	}
	if err != nil {
		return err
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// parse json
	var response VersionResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	ch <- prometheus.MustNewConstMetric(
		version, prometheus.GaugeValue, 1, response.Data.Version, response.Data.Repoid, response.Data.Release,
	)

	return nil
}

func (e *Exporter) getNodeSubscriptionMetrics(ch chan<- prometheus.Metric) error {
	req, err := http.NewRequest("GET", e.endpoint+nodeApi+"/localhost/subscription", nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	if *loglevel == "debug" {
		log.Printf("DEBUG: Request URL: %s", req.URL)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		log.Printf("Error closing response body: %v", err)
	}
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	if *loglevel == "debug" {
		log.Printf("DEBUG: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	var raw struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}

	// default values
	statusStr := ""
	productName := "unknown"
	dueTs := int64(0)

	if v, ok := raw.Data["status"]; ok && v != nil {
		statusStr = fmt.Sprintf("%v", v)
	}
	if v, ok := raw.Data["productname"]; ok && v != nil {
		productName = fmt.Sprintf("%v", v)
	}
	if v, ok := raw.Data["nextduedate"]; ok && v != nil {
		if s, ok := v.(string); ok {
			t, err := time.Parse("2006-01-02", s)
			if err == nil {
				dueTs = t.Unix()
			}
		}
	}

	ch <- prometheus.MustNewConstMetric(
		subscription_info, prometheus.GaugeValue, 1, productName, statusStr,
	)

	ch <- prometheus.MustNewConstMetric(
		subscription_due_timestamp_seconds, prometheus.GaugeValue, float64(dueTs), productName,
	)

	// Emit a metric for each possible status with 1/0
	statuses := []string{"new", "notfound", "active", "invalid", "expired", "suspended"}
	for _, s := range statuses {
		val := 0.0
		if statusStr == s {
			val = 1.0
		}
		ch <- prometheus.MustNewConstMetric(
			subscription_status, prometheus.GaugeValue, val, s,
		)
	}

	return nil
}

func (e *Exporter) getNodeMetrics(ch chan<- prometheus.Metric) error {
	// NOTE: According to the api documentation, we have to provide the node name (won't work with the node ip),
	// but it seems to work with any name, so we just use "localhost" here.
	// see: https://pbs.proxmox.com/docs/api-viewer/index.html#/nodes/{node}
	req, err := http.NewRequest("GET", e.endpoint+nodeApi+"/localhost/status", nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Request URL: %s", req.URL)
		//log.Printf("DEBUG: Request Header: %s", vmID)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		log.Printf("Error closing response body: %v", err)
	}
	if err != nil {
		return err
	}

	// skip host metrics if the token lacks Sys.Audit, instead of failing the whole scrape
	if resp.StatusCode == http.StatusForbidden {
		logMissingPermission(e.endpoint, nodeApi+"/localhost/status", "Sys.Audit on /system/status", "host metrics")
		return nil
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response HostResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// set host metrics
	ch <- prometheus.MustNewConstMetric(
		host_cpu_usage, prometheus.GaugeValue, float64(response.Data.CPU),
	)
	ch <- prometheus.MustNewConstMetric(
		host_memory_free, prometheus.GaugeValue, float64(response.Data.Mem.Free),
	)
	ch <- prometheus.MustNewConstMetric(
		host_memory_total, prometheus.GaugeValue, float64(response.Data.Mem.Total),
	)
	ch <- prometheus.MustNewConstMetric(
		host_memory_used, prometheus.GaugeValue, float64(response.Data.Mem.Used),
	)
	ch <- prometheus.MustNewConstMetric(
		host_swap_free, prometheus.GaugeValue, float64(response.Data.Swap.Free),
	)
	ch <- prometheus.MustNewConstMetric(
		host_swap_total, prometheus.GaugeValue, float64(response.Data.Swap.Total),
	)
	ch <- prometheus.MustNewConstMetric(
		host_swap_used, prometheus.GaugeValue, float64(response.Data.Swap.Used),
	)
	ch <- prometheus.MustNewConstMetric(
		host_disk_available, prometheus.GaugeValue, float64(response.Data.Disk.Avail),
	)
	ch <- prometheus.MustNewConstMetric(
		host_disk_total, prometheus.GaugeValue, float64(response.Data.Disk.Total),
	)
	ch <- prometheus.MustNewConstMetric(
		host_disk_used, prometheus.GaugeValue, float64(response.Data.Disk.Used),
	)
	ch <- prometheus.MustNewConstMetric(
		host_uptime, prometheus.GaugeValue, float64(response.Data.Uptime),
	)
	ch <- prometheus.MustNewConstMetric(
		host_io_wait, prometheus.GaugeValue, float64(response.Data.Wait),
	)
	ch <- prometheus.MustNewConstMetric(
		host_load1, prometheus.GaugeValue, float64(response.Data.Load[0]),
	)
	ch <- prometheus.MustNewConstMetric(
		host_load5, prometheus.GaugeValue, float64(response.Data.Load[1]),
	)
	ch <- prometheus.MustNewConstMetric(
		host_load15, prometheus.GaugeValue, float64(response.Data.Load[2]),
	)

	return nil
}

func (e *Exporter) getDatastoreMetric(datastore Datastore, ch chan<- prometheus.Metric) error {
	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: --Store %s", datastore.Store)
		log.Printf("DEBUG: --Avail %d", datastore.Avail)
		log.Printf("DEBUG: --Total %d", datastore.Total)
		log.Printf("DEBUG: --Used %d", datastore.Used)
	}

	// set datastore metrics
	ch <- prometheus.MustNewConstMetric(
		available, prometheus.GaugeValue, float64(datastore.Avail), datastore.Store,
	)
	ch <- prometheus.MustNewConstMetric(
		size, prometheus.GaugeValue, float64(datastore.Total), datastore.Store,
	)
	ch <- prometheus.MustNewConstMetric(
		used, prometheus.GaugeValue, float64(datastore.Used), datastore.Store,
	)
	if datastore.EstimatedFullDate > 0 {
		ch <- prometheus.MustNewConstMetric(
			datastore_estimated_full_timestamp_seconds, prometheus.GaugeValue, float64(datastore.EstimatedFullDate), datastore.Store,
		)
	}
	if datastore.GCStatus != nil {
		ch <- prometheus.MustNewConstMetric(
			datastore_index_data_bytes, prometheus.GaugeValue, float64(datastore.GCStatus.IndexDataBytes), datastore.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			datastore_disk_bytes, prometheus.GaugeValue, float64(datastore.GCStatus.DiskBytes), datastore.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			datastore_disk_chunks, prometheus.GaugeValue, float64(datastore.GCStatus.DiskChunks), datastore.Store,
		)
		ch <- prometheus.MustNewConstMetric(
			datastore_index_file_count, prometheus.GaugeValue, float64(datastore.GCStatus.IndexFileCount), datastore.Store,
		)
	}

	// get namespaces of datastore
	req, err := http.NewRequest("GET", e.endpoint+datastoreApi+"/"+datastore.Store+"/namespace", nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: --Request URL: %s", req.URL)
		//log.Printf("DEBUG: --Request Header: %s", vmID)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		log.Printf("Error closing response body: %v", err)
	}
	if err != nil {
		return err
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		if resp.StatusCode == http.StatusForbidden {
			logMissingPermission(e.endpoint, datastoreApi+"/"+datastore.Store+"/namespace", "Datastore.Audit on /datastore/"+datastore.Store, "snapshot metrics of datastore "+datastore.Store)
			return nil
		}
		if resp.StatusCode == 400 {
			// check if datastore is being deleted
			isBeingDeleted, err := regexp.MatchString("(?i)datastore is being deleted", string(body[:]))
			if err != nil {
				return err
			}
			if isBeingDeleted {
				log.Printf("INFO: Datastore: %s is being deleted, Skip scrape datastore metric", datastore.Store)
				return nil
			}
			isMaintenance, err := regexp.MatchString("(?i)offline maintenance mode", string(body[:]))
			if err != nil {
				return err
			}
			if isMaintenance {
				log.Printf("INFO: Datastore: %s is in maintenance mode, Skip scrape datastore metric", datastore.Store)
				return nil
			}
			isUnmounted, err := regexp.MatchString("(?i)is not mounted", string(body[:]))
			if err != nil {
				return err
			}
			if isUnmounted {
				log.Printf("INFO: Datastore: %s is unmounted, Skip scrape datastore metric", datastore.Store)
				return nil
			}
		}
		return fmt.Errorf("ERROR: --Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: --Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response NamespaceResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// for each namespace collect metrics
	for _, namespace := range response.Data {
		err := e.getNamespaceMetric(datastore.Store, namespace.Namespace, ch)
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *Exporter) getNamespaceMetric(datastore string, namespace string, ch chan<- prometheus.Metric) error {
	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: ----Namespace %s", namespace)
	}

	// get snapshots of datastore
	req, err := http.NewRequest("GET", e.endpoint+datastoreApi+"/"+datastore+"/snapshots?ns="+namespace, nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: ----Request URL: %s", req.URL)
		//log.Printf("DEBUG: ----Request Header: %s", vmID)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		log.Printf("Error closing response body: %v", err)
	}
	if err != nil {
		return err
	}

	// skip namespaces the token lacks permissions for
	if resp.StatusCode == http.StatusForbidden {
		logMissingPermission(e.endpoint, datastoreApi+"/"+datastore+"/snapshots?ns="+namespace, "Datastore.Audit on /datastore/"+datastore, "snapshot metrics of namespace "+namespace)
		return nil
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: ----Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: ----Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response SnapshotResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// set total snapshot metrics
	ch <- prometheus.MustNewConstMetric(
		snapshot_count, prometheus.GaugeValue, float64(len(response.Data)), datastore, namespace,
	)

	// set snapshot metrics per vm
	vmNameMapping := make(map[string]string)
	vmCount := make(map[string]int)
	for _, snapshot := range response.Data {
		// get vm name from snapshot
		vmID := snapshot.BackupID
		vmNameMapping[vmID] = snapshot.VMName
		vmCount[vmID]++
	}

	// set snapshot metrics per vm
	for vmID, count := range vmCount {
		ch <- prometheus.MustNewConstMetric(
			snapshot_vm_count, prometheus.GaugeValue, float64(count), datastore, namespace, vmID, vmNameMapping[vmID],
		)

		// find last snapshot with backupID
		lastTimeStamp, lastVerify, err := findLastSnapshotWithBackupID(response, vmID)
		if err != nil {
			return err
		}
		lastVerifyBool := 0
		if lastVerify == "ok" {
			lastVerifyBool = 1
		}
		ch <- prometheus.MustNewConstMetric(
			snapshot_vm_last_timestamp, prometheus.GaugeValue, float64(lastTimeStamp), datastore, namespace, vmID, vmNameMapping[vmID],
		)
		ch <- prometheus.MustNewConstMetric(
			snapshot_vm_last_verify, prometheus.GaugeValue, float64(lastVerifyBool), datastore, namespace, vmID, vmNameMapping[vmID],
		)
	}

	return nil
}

func findLastSnapshotWithBackupID(response SnapshotResponse, backupID string) (int64, string, error) {
	// find biggest value of backupTime of backupID in response array
	var lastTimeStamp int64
	var lastVerify string
	for _, snapshot := range response.Data {
		if snapshot.BackupID == backupID {
			if snapshot.BackupTime > lastTimeStamp {
				lastTimeStamp = snapshot.BackupTime
				lastVerify = snapshot.Verification.State
			}
		}
	}

	// if lastTimeStamp is still 0, no snapshot was found
	if lastTimeStamp != 0 {
		return lastTimeStamp, lastVerify, nil
	}

	return 0, "", fmt.Errorf("ERROR: No snapshot found with backupID %s", backupID)
}

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Printf("PBS Exporter Version: %s, Commit: %s, Build Time: %s\n", Version, Commit, BuildTime)
		os.Exit(0)
	}

	// log build information
	log.Printf("INFO: Starting PBS Exporter %s, commit %s, built at %s", Version, Commit, BuildTime)

	// if env variable is set, it will overwrite defaults or flags
	if os.Getenv("PBS_LOGLEVEL") != "" {
		*loglevel = os.Getenv("PBS_LOGLEVEL")
	}
	if os.Getenv("PBS_ENDPOINT") != "" {
		*endpoint = os.Getenv("PBS_ENDPOINT")
	}
	if os.Getenv("PBS_USERNAME") != "" {
		*username = os.Getenv("PBS_USERNAME")
	} else {
		if os.Getenv("PBS_USERNAME_FILE") != "" {
			*username = ReadSecretFile(os.Getenv("PBS_USERNAME_FILE"))
		}
	}
	if os.Getenv("PBS_API_TOKEN_NAME") != "" {
		*apitokenname = os.Getenv("PBS_API_TOKEN_NAME")
	} else {
		if os.Getenv("PBS_API_TOKEN_NAME_FILE") != "" {
			*apitokenname = ReadSecretFile(os.Getenv("PBS_API_TOKEN_NAME_FILE"))
		}
	}
	if os.Getenv("PBS_API_TOKEN") != "" {
		*apitoken = os.Getenv("PBS_API_TOKEN")
	} else {
		if os.Getenv("PBS_API_TOKEN_FILE") != "" {
			*apitoken = ReadSecretFile(os.Getenv("PBS_API_TOKEN_FILE"))
		}
	}
	if os.Getenv("PBS_TIMEOUT") != "" {
		*timeout = os.Getenv("PBS_TIMEOUT")
	}
	if os.Getenv("PBS_INSECURE") != "" {
		*insecure = os.Getenv("PBS_INSECURE")
	}
	if os.Getenv("PBS_METRICS_PATH") != "" {
		*metricsPath = os.Getenv("PBS_METRICS_PATH")
	}
	if os.Getenv("PBS_LISTEN_ADDRESS") != "" {
		*listenAddress = os.Getenv("PBS_LISTEN_ADDRESS")
	}

	// convert flags
	insecureBool, err := strconv.ParseBool(*insecure)
	if err != nil {
		log.Fatalf("ERROR: Unable to parse insecure: %s", err)
	}

	// set insecure
	if insecureBool {
		tr.TLSClientConfig.InsecureSkipVerify = true
	}

	// set timeout
	timeoutDuration, err := time.ParseDuration(*timeout)
	if err != nil {
		log.Fatalf("ERROR: Unable to parse timeout: %s", err)
	}
	client.Timeout = timeoutDuration

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Using connection endpoint: %s", *endpoint)
		log.Printf("DEBUG: Using connection username: %s", *username)
		log.Printf("DEBUG: Using connection apitoken: %s", *apitoken)
		log.Printf("DEBUG: Using connection apitokenname: %s", *apitokenname)
		log.Printf("DEBUG: Using connection timeout: %s", client.Timeout)
		log.Printf("DEBUG: Using connection insecure: %t", tr.TLSClientConfig.InsecureSkipVerify)
		log.Printf("DEBUG: Using metrics path: %s", *metricsPath)
		log.Printf("DEBUG: Using listen address: %s", *listenAddress)
	}

	if *endpoint != "" {
		log.Printf("INFO: Using fix connection endpoint: %s", *endpoint)
	}
	log.Printf("INFO: Listening on: %s", *listenAddress)
	log.Printf("INFO: Metrics path: %s", *metricsPath)

	// start http server
	http.HandleFunc(*metricsPath, func(w http.ResponseWriter, r *http.Request) {
		target := ""

		// if endpoint was not set as flag or env variable, we try to get it from "target" query parameter
		if *endpoint != "" {
			target = *endpoint
		} else {
			target = r.URL.Query().Get("target")
			if target == "" {
				// if target is not set, we use the default
				target = "http://localhost:8007"
			}
		}

		// debug
		if *loglevel == "debug" {
			log.Printf("DEBUG: Using connection endpoint %s", strings.ReplaceAll(strings.ReplaceAll(target, "\n", ""), "\r", ""))
		}

		exporter := NewExporter(target, *username, *apitoken, *apitokenname)

		// catch if register of exporter fails
		err := prometheus.Register(exporter)
		if err != nil {
			// if register fails, we log the error and return
			log.Printf("ERROR: %s", err)
		}
		promhttp.Handler().ServeHTTP(w, r) // Serve the metrics
		prometheus.Unregister(exporter)    // Clean up after serving
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte(`<html>
			<head><title>PBS Exporter</title></head>
			<body>
			<h1>Proxmox Backup Server Exporter</h1>
			<p><a href='` + *metricsPath + `'>Metrics</a></p>
			</body>
			</html>`))
		if err != nil {
			log.Printf("ERROR: Failed to write response: %s", err)
		}
	})

	server := &http.Server{
		Addr:         *listenAddress,
		Handler:      nil,
		ReadTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
	}
	log.Fatal(server.ListenAndServe())
}
