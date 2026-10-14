package main

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// UPID with start time 0x6553F100 = 1700000000
const testUpid = "UPID:pbs:000012AB:0000ABCD:00000001:6553F100:syncjob:s-1:root@pam:"

var testResponses = map[string]string{
	versionApi:                          `{"data":{"release":"4.0","repoid":"abc","version":"4.0.1"}}`,
	datastoreUsageApi:                   `{"data":[{"store":"ds1","avail":100,"total":300,"used":200,"estimated-full-date":1800000000,"gc-status":{"index-data-bytes":1000,"disk-bytes":250,"disk-chunks":40,"index-file-count":12}},{"store":"ds2","avail":1,"total":2,"used":1}]}`,
	datastoreApi + "/ds1/namespace":     `{"data":[{"ns":""}]}`,
	datastoreApi + "/ds1/snapshots":     `{"data":[{"backup-id":"100","backup-time":1700000000,"comment":"vm1","verification":{"state":"ok"}}]}`,
	datastoreApi + "/ds2/namespace":     `{"data":[]}`,
	nodeApi + "/localhost/status":       `{"data":{"cpu":0.1,"memory":{"free":1,"total":2,"used":1},"swap":{"free":1,"total":2,"used":1},"root":{"avail":1,"total":2,"used":1},"loadavg":[0.1,0.2,0.3],"uptime":10,"wait":0.01}}`,
	nodeApi + "/localhost/subscription": `{"data":{"status":"notfound"}}`,
	datastoreApi:                        `{"data":[{"store":"ds1","backend-type":"filesystem","mount-status":"nonremovable"},{"store":"ds2","backend-type":"s3","mount-status":"mounted","maintenance":"type=read-only,message=upgrade"}]}`,
	gcApi:                               `{"data":[{"store":"ds1","last-run-state":"OK","last-run-endtime":1700000500,"duration":120,"next-run":1700086400,"upid":"` + testUpid + `","removed-bytes":10,"removed-chunks":2,"pending-bytes":20,"pending-chunks":3,"removed-bad":0,"still-bad":1,"disk-bytes":250,"disk-chunks":40,"index-data-bytes":1000,"index-file-count":12},{"store":"ds2","removed-bytes":0},{"store":"ds3","next-run":1700086400,"upid":"UPID:pbs:0000009D:048A30A3:000018BD:6553F100:garbage_collection:ds3:root@pam:","removed-bytes":5,"disk-bytes":7}]}`,
	syncApi: `{"data":[
		{"id":"s-ok","store":"ds1","remote":"r1","remote-store":"rs1","last-run-state":"OK","last-run-endtime":1700000300,"last-run-upid":"` + testUpid + `","next-run":1700086400},
		{"id":"s-warn","store":"ds1","remote":"r1","remote-store":"rs1","sync-direction":"push","last-run-state":"WARNINGS: 2","last-run-endtime":1700000300,"last-run-upid":"` + testUpid + `"},
		{"id":"s-err","store":"ds1","remote":"r1","remote-store":"rs1","last-run-state":"connection refused","last-run-endtime":1700000300,"last-run-upid":"` + testUpid + `"},
		{"id":"s-run","store":"ds1","remote":"r1","remote-store":"rs1","last-run-upid":"` + testUpid + `"},
		{"id":"s-new","store":"ds1","remote":"r1","remote-store":"rs1"}
	]}`,
	pruneApi: `{"data":[{"id":"p-1","store":"ds1","ns":"prod","last-run-state":"OK","last-run-endtime":1700000060,"last-run-upid":"` + testUpid + `"}]}`,
	nodeApi + "/localhost/tasks": `{"total":4,"data":[
		{"upid":"x","node":"pbs","pid":1,"pstart":1,"starttime":1700000000,"user":"root@pam","worker_type":"verificationjob","worker_id":"MAIN-DATASTORE:v-ce221dc3-9d80"},
		{"upid":"x","node":"pbs","pid":1,"pstart":1,"starttime":1700000100,"user":"root@pam","worker_type":"garbage_collection","worker_id":"MAIN-DATASTORE"},
		{"upid":"x","node":"pbs","pid":1,"pstart":1,"starttime":1700000500,"user":"root@pam","worker_type":"backup","worker_id":"ds1:vm/100"},
		{"upid":"x","node":"pbs","pid":1,"pstart":1,"starttime":1700000200,"user":"root@pam","worker_type":"backup","worker_id":"ds1:vm/100"}
	]}`,
	verifyApi: `{"data":[{"id":"v-1","store":"ds1","last-run-state":"verification failed","last-run-endtime":1700003600,"last-run-upid":"` + testUpid + `"}]}`,
}

// collect runs the exporter against a fake PBS and returns all metrics as "name{labels}" -> value.
func collect(t *testing.T, statusOverrides map[string]int) map[string]float64 {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if code, ok := statusOverrides[path]; ok {
			w.WriteHeader(code)
			return
		}
		body, ok := testResponses[path]
		if !ok {
			t.Errorf("unexpected request: %s", path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewExporter(server.URL, "root@pam", "token", "pbs-exporter"))
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %s", err)
	}

	metrics := map[string]float64{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			labels := []string{}
			for _, label := range metric.GetLabel() {
				labels = append(labels, label.GetName()+"=\""+label.GetValue()+"\"")
			}
			sort.Strings(labels)
			metrics[family.GetName()+"{"+strings.Join(labels, ",")+"}"] = metric.GetGauge().GetValue()
		}
	}
	return metrics
}

func assertMetrics(t *testing.T, metrics map[string]float64, expected map[string]float64, absent []string) {
	t.Helper()
	for key, want := range expected {
		got, ok := metrics[key]
		if !ok {
			t.Errorf("missing metric %s", key)
			continue
		}
		if got != want {
			t.Errorf("metric %s = %v, want %v", key, got, want)
		}
	}
	for _, key := range absent {
		if _, ok := metrics[key]; ok {
			t.Errorf("unexpected metric %s", key)
		}
	}
}

func TestCollect(t *testing.T) {
	metrics := collect(t, nil)

	assertMetrics(t, metrics, map[string]float64{
		`pbs_up{}`:                  1,
		`pbs_used{datastore="ds1"}`: 200,
		`pbs_snapshot_count{datastore="ds1",namespace=""}`: 1,

		// datastore info / usage
		`pbs_datastore_info{backend_type="filesystem",datastore="ds1",maintenance_mode="none",mount_status="nonremovable"}`: 1,
		`pbs_datastore_info{backend_type="s3",datastore="ds2",maintenance_mode="read-only",mount_status="mounted"}`:         1,
		`pbs_datastore_estimated_full_timestamp_seconds{datastore="ds1"}`:                                                   1800000000,
		`pbs_datastore_index_data_bytes{datastore="ds1"}`:                                                                   1000,
		`pbs_datastore_disk_bytes{datastore="ds1"}`:                                                                         250,
		`pbs_datastore_disk_chunks{datastore="ds1"}`:                                                                        40,
		`pbs_datastore_index_file_count{datastore="ds1"}`:                                                                   12,

		// garbage collection
		`pbs_gc_last_run_status{datastore="ds1",status="ok"}`:      1,
		`pbs_gc_last_run_status{datastore="ds1",status="warning"}`: 0,
		`pbs_gc_last_run_status{datastore="ds1",status="error"}`:   0,
		`pbs_gc_last_run_end_timestamp_seconds{datastore="ds1"}`:   1700000500,
		`pbs_gc_last_run_duration_seconds{datastore="ds1"}`:        120,
		`pbs_gc_next_run_timestamp_seconds{datastore="ds1"}`:       1700086400,
		`pbs_gc_last_run_start_timestamp_seconds{datastore="ds1"}`: 1700000000,
		`pbs_gc_running{datastore="ds1"}`:                          0,
		`pbs_gc_running{datastore="ds2"}`:                          0,
		// running gc: upid and statistics of the last finished gc, but no last-run fields
		`pbs_gc_running{datastore="ds3"}`:                          1,
		`pbs_gc_last_run_start_timestamp_seconds{datastore="ds3"}`: 1700000000,
		`pbs_gc_next_run_timestamp_seconds{datastore="ds3"}`:       1700086400,
		`pbs_gc_removed_bytes{datastore="ds3"}`:                    5,
		`pbs_gc_removed_bytes{datastore="ds1"}`:                    10,
		`pbs_gc_removed_chunks{datastore="ds1"}`:                   2,
		`pbs_gc_pending_bytes{datastore="ds1"}`:                    20,
		`pbs_gc_pending_chunks{datastore="ds1"}`:                   3,
		`pbs_gc_removed_bad_chunks{datastore="ds1"}`:               0,
		`pbs_gc_still_bad_chunks{datastore="ds1"}`:                 1,

		// sync jobs
		`pbs_sync_job_info{datastore="ds1",direction="pull",job_id="s-ok",remote="r1",remote_store="rs1"}`:   1,
		`pbs_sync_job_info{datastore="ds1",direction="push",job_id="s-warn",remote="r1",remote_store="rs1"}`: 1,
		`pbs_sync_job_last_run_status{datastore="ds1",job_id="s-ok",status="ok"}`:                            1,
		`pbs_sync_job_last_run_status{datastore="ds1",job_id="s-ok",status="error"}`:                         0,
		`pbs_sync_job_last_run_status{datastore="ds1",job_id="s-warn",status="warning"}`:                     1,
		`pbs_sync_job_last_run_status{datastore="ds1",job_id="s-warn",status="ok"}`:                          0,
		`pbs_sync_job_last_run_status{datastore="ds1",job_id="s-err",status="error"}`:                        1,
		`pbs_sync_job_last_run_start_timestamp_seconds{datastore="ds1",job_id="s-ok"}`:                       1700000000,
		`pbs_sync_job_last_run_end_timestamp_seconds{datastore="ds1",job_id="s-ok"}`:                         1700000300,
		`pbs_sync_job_last_run_duration_seconds{datastore="ds1",job_id="s-ok"}`:                              300,
		`pbs_sync_job_next_run_timestamp_seconds{datastore="ds1",job_id="s-ok"}`:                             1700086400,
		`pbs_sync_job_running{datastore="ds1",job_id="s-ok"}`:                                                0,
		`pbs_sync_job_running{datastore="ds1",job_id="s-run"}`:                                               1,
		`pbs_sync_job_last_run_start_timestamp_seconds{datastore="ds1",job_id="s-run"}`:                      1700000000,
		`pbs_sync_job_running{datastore="ds1",job_id="s-new"}`:                                               0,

		// prune / verify jobs
		`pbs_prune_job_info{datastore="ds1",job_id="p-1",namespace="prod"}`:           1,
		`pbs_prune_job_last_run_status{datastore="ds1",job_id="p-1",status="ok"}`:     1,
		`pbs_prune_job_last_run_duration_seconds{datastore="ds1",job_id="p-1"}`:       60,
		`pbs_verify_job_info{datastore="ds1",job_id="v-1"}`:                           1,
		`pbs_verify_job_last_run_status{datastore="ds1",job_id="v-1",status="error"}`: 1,
		`pbs_verify_job_last_run_duration_seconds{datastore="ds1",job_id="v-1"}`:      3600,

		// running tasks, duplicates keep the earliest start time
		`pbs_task_running_start_timestamp_seconds{worker_id="MAIN-DATASTORE:v-ce221dc3-9d80",worker_type="verificationjob"}`: 1700000000,
		`pbs_task_running_start_timestamp_seconds{worker_id="MAIN-DATASTORE",worker_type="garbage_collection"}`:              1700000100,
		`pbs_task_running_start_timestamp_seconds{worker_id="ds1:vm/100",worker_type="backup"}`:                              1700000200,
	}, []string{
		// ds2 has no estimate and no gc-status in datastore-usage
		`pbs_datastore_estimated_full_timestamp_seconds{datastore="ds2"}`,
		`pbs_datastore_disk_bytes{datastore="ds2"}`,
		// gc on ds2 never ran
		`pbs_gc_last_run_status{datastore="ds2",status="ok"}`,
		`pbs_gc_removed_bytes{datastore="ds2"}`,
		`pbs_gc_last_run_start_timestamp_seconds{datastore="ds2"}`,
		`pbs_gc_last_run_status{datastore="ds3",status="ok"}`,
		`pbs_gc_last_run_status{datastore="ds3",status="error"}`,
		`pbs_gc_last_run_duration_seconds{datastore="ds3"}`,
		// running and never-run jobs have no last run result
		`pbs_sync_job_last_run_status{datastore="ds1",job_id="s-run",status="ok"}`,
		`pbs_sync_job_last_run_duration_seconds{datastore="ds1",job_id="s-run"}`,
		`pbs_sync_job_last_run_status{datastore="ds1",job_id="s-new",status="ok"}`,
		`pbs_sync_job_last_run_start_timestamp_seconds{datastore="ds1",job_id="s-new"}`,
		`pbs_sync_job_next_run_timestamp_seconds{datastore="ds1",job_id="s-warn"}`,
	})
}

func TestCollectJobEndpointForbidden(t *testing.T) {
	metrics := collect(t, map[string]int{syncApi: http.StatusForbidden})

	assertMetrics(t, metrics, map[string]float64{
		`pbs_up{}`: 1,
		`pbs_prune_job_info{datastore="ds1",job_id="p-1",namespace="prod"}`: 1,
	}, []string{
		`pbs_sync_job_running{datastore="ds1",job_id="s-ok"}`,
	})
}

func TestCollectNodeStatusForbidden(t *testing.T) {
	// token without Sys.Audit on /system/status: host metrics are skipped, everything else is collected
	metrics := collect(t, map[string]int{nodeApi + "/localhost/status": http.StatusForbidden})

	assertMetrics(t, metrics, map[string]float64{
		`pbs_up{}`:                  1,
		`pbs_used{datastore="ds1"}`: 200,
		`pbs_host_subscription_info{productname="unknown",status="notfound"}`: 1,
		`pbs_gc_running{datastore="ds1"}`:                                     0,
		`pbs_sync_job_running{datastore="ds1",job_id="s-ok"}`:                 0,
	}, []string{
		`pbs_host_cpu_usage{}`,
		`pbs_host_memory_used{}`,
	})
}

func TestCollectSnapshotsForbidden(t *testing.T) {
	// token without access to the snapshots of a namespace: only these snapshot metrics are skipped
	metrics := collect(t, map[string]int{datastoreApi + "/ds1/snapshots": http.StatusForbidden})

	assertMetrics(t, metrics, map[string]float64{
		`pbs_up{}`:                  1,
		`pbs_used{datastore="ds1"}`: 200,
		`pbs_host_cpu_usage{}`:      0.1,
	}, []string{
		`pbs_snapshot_count{datastore="ds1",namespace=""}`,
	})
}

func TestCollectUnauthorized(t *testing.T) {
	// an invalid token still fails the scrape, as before
	metrics := collect(t, map[string]int{versionApi: http.StatusUnauthorized})

	assertMetrics(t, metrics, map[string]float64{
		`pbs_up{}`: 0,
	}, []string{
		`pbs_used{datastore="ds1"}`,
	})
}

func TestLogMissingPermissionOnce(t *testing.T) {
	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	logMissingPermission("https://pbs-log-test:8007", "/api2/json/nodes/localhost/status", "Sys.Audit on /system/status", "host metrics")
	logMissingPermission("https://pbs-log-test:8007", "/api2/json/nodes/localhost/status", "Sys.Audit on /system/status", "host metrics")
	logMissingPermission("https://pbs-log-test:8007", "/api2/json/admin/sync", "Remote.Audit on /remote", "sync job metrics")

	got := strings.Count(buf.String(), "WARN: Missing permission")
	if got != 2 {
		t.Errorf("got %d warnings, want 2 (one per api path):\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "WARN: Missing permission Sys.Audit on /system/status for /api2/json/nodes/localhost/status on endpoint https://pbs-log-test:8007, skipping host metrics") {
		t.Errorf("unexpected log output:\n%s", buf.String())
	}
}

func TestParseUpidStartTime(t *testing.T) {
	tests := []struct {
		upid string
		want int64
		ok   bool
	}{
		{testUpid, 1700000000, true},
		{"UPID:pbs:000012AB:0000ABCD:00000001:6553F100:syncjob:r1\\x3ads1\\x3as-1:root@pam!token:", 1700000000, true},
		{"", 0, false},
		{"UPID:pbs:1:2", 0, false},
		{"UPID:pbs:000012AB:0000ABCD:00000001:zzzz:syncjob:s-1:root@pam:", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseUpidStartTime(tt.upid)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseUpidStartTime(%q) = %d, %t, want %d, %t", tt.upid, got, ok, tt.want, tt.ok)
		}
	}
}

func TestJobStatusCategory(t *testing.T) {
	tests := map[string]string{
		"OK":                 "ok",
		"WARNINGS: 3":        "warning",
		"connection refused": "error",
		"unknown":            "error",
	}
	for state, want := range tests {
		if got := jobStatusCategory(state); got != want {
			t.Errorf("jobStatusCategory(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestParseMaintenanceType(t *testing.T) {
	tests := map[string]string{
		"":                               "none",
		"type=offline":                   "offline",
		"type=read-only,message=upgrade": "read-only",
		"message=upgrade,type=unmount":   "unmount",
		"offline,message=x":              "offline",
	}
	for maintenance, want := range tests {
		if got := parseMaintenanceType(maintenance); got != want {
			t.Errorf("parseMaintenanceType(%q) = %q, want %q", maintenance, got, want)
		}
	}
}
