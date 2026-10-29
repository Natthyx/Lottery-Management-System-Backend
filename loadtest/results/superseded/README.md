# Superseded outputs (kept as evidence, not used for headline numbers)

## l3_port_forwarder_incident/

The first full run against the fixed code stopped during L3. In `l3_interrupted/dup5_retry_n1000/run1`
(1000 users × 5 parallel requests, 200 VUs) k6 recorded 3239 network errors and only 415 users got
their booking. `run.sh` then aborted on `curl: (52) Empty reply from server` (`run_log.txt`).

What the evidence shows:
* The app did not fail. `lt_app` was still running with `RestartCount=0`, `OOMKilled=false`, and no
  error-level log lines.
* Immediately afterwards, requests from the host to `127.0.0.1:18080` got no response (`000`), while the same
  requests from a container on the compose network (`http://app:8080/health`, `/events`) returned 200.
  The host side recovered on its own within minutes. The failing component was therefore Docker Desktop's
  host port forwarding, not the application.
* Even in that run, no invariant was broken: no user had more than one booking, no duplicates, no over-capacity,
  and DB rows = 201s seen by the client (`sql_run1.txt`). Users simply did not get a response.
* The identical scenario was then re-run 6 times from the host. All 6 were clean: 1000 × 201, 4000 × already_booked,
  0 errors, about 2 s ([repro/](repro/) has the last one).

**It happened a second time** (`l3_interrupted_2/`, `run_log_2.txt`), in `dup5_noretry_n200` runs 2-3: 306 and 205
requests failed. This time `k6/lib.js` logged every transport error, and all were `code=1000 err="EOF"` (the connection
closed with no response).

**Root cause (confirmed):** Docker Desktop's VM-side port forwarder logged 3,755 lines like
`[init.vpnkitcontrol.multiplex] forwarding traffic to backend tcp/{172.18.0.4 8080}: dial tcp 172.18.0.4:8080: connect: cannot assign requested address`
at both failure times ([docker_vm_log_excerpt.txt](l3_port_forwarder_incident/docker_vm_log_excerpt.txt)).
That is ephemeral-port exhaustion inside the Docker VM. Its range is 55000-65535 (10,536 ports) and TIME_WAIT lasts 60 s.
One L3 run at N=200 left 1,001 sockets in TIME_WAIT, so a series of back-to-back runs exhausts the range. The forwarder
then accepts the host connection and closes it, which the client sees as EOF / empty reply. The app never sees those
requests.

**Fix (harness only):** `run_k6` now waits until the VM's TIME_WAIT count is below 2,000 before every k6 run
(`wait_for_ports` in `lib/common.sh`; every wait is logged to `results/cooldown.log`). L3 to L9 were then re-run.
