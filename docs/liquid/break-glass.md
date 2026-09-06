<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Break-glass node access

Break-glass access is exceptional, certificate-only, limited to one hour, and visible
in lake's signed audit stream. There are no host passwords, static authorized keys, or
permanent SSH service. A serial console supplied by the infrastructure owner is the
last resort when Kubernetes or lake is unavailable; its use must be entered into the
incident record because k0s cannot audit an out-of-band console.

## Open an access window

1. Record an incident or change reference and a specific reason for host access.
2. Ask lake PKI's `issue_ssh_user_certificate` operation for an Ed25519 OpenSSH user
   certificate. Use the human operator as the certificate principal and request no
   more than 3600 seconds. Save the certificate next to the operator's private key.
3. Create a cluster-scoped `BreakGlass` object with the same principal and reason:

   ```yaml
   apiVersion: liquid.k0sproject.io/v1alpha1
   kind: BreakGlass
   metadata:
     name: incident-1842-controller-a
   spec:
     node: controller-a
     principal: operator@example.com
     reason: "incident-1842: inspect etcd disk state"
     ttlSeconds: 1800
   ```

4. Wait for `status.phase: Open`. The controller rejects an empty reason, unknown
   node, missing principal, or lifetime outside 1 through 3600 seconds. The node also
   enforces the maximum in its signed machine configuration. Requests are immutable
   after the controller accepts them; create a new request instead of editing one.
5. Connect as the existing locked root account while presenting the lake-issued user
   certificate. The certificate principal, rather than a static key, authorizes the
   session:

   ```sh
   ssh -i operator_key -o CertificateFile=operator_key-cert.pub root@controller-a
   ```

The node records the audit-open event before it starts `sshd`; failure to reach lake
keeps SSH disabled. `sshd` accepts public-key certificates from only the customer CA
at `/etc/ssh/trusted-user-ca-keys.pem`, reads the one active principal from
`/run/k0s/break-glass-principals`, disables password and keyboard-interactive login,
and forces the audited session wrapper.

## Close and verify

The node agent stops `sshd` when the TTL expires, removes the principal and active
request files, records the close event, and changes the resource status to `Closed`.
Closing an interactive shell records a separate session-close event. Deleting the CR
does not extend an access window and is not a substitute for waiting for `Closed`.

Verify all four records in lake's signed audit stream:

- `break_glass_open`, before `sshd` starts;
- `break_glass_login`, before the shell starts;
- `break_glass_session_close`, after that shell exits;
- `break_glass_close`, after `sshd` stops at expiry.

Each event contains the node, `BreakGlass` name, principal, exact reason, and UTC
timestamp. The node optionally authenticates with the lake mTLS pair at
`/etc/k0s/lake/tls.crt` and `/etc/k0s/lake/tls.key`. If either file is present, both
must be present.

Lake's signed audit stream is the interim authoritative record. The events will also
be committed to lake's transparency log after lake issues #34 through #39 land. Until
then, preserve the signed audit response and incident reference according to the
operator's retention policy.

## Failure cases

- If lake PKI is unavailable, no certificate can be issued. Use the serial console only
  for an incident that cannot wait, and record that access out of band.
- If lake audit is unavailable before opening, the node does not start `sshd`.
- If audit becomes unavailable while closing, the node still stops `sshd` first and
  retries through the supervised agent restart; access never remains open for logging.
- If Kubernetes is unavailable but a window is already open, the host-local expiry is
  still enforced from `/run/k0s/break-glass.json`.
- Never extend an active window. Let it close and create a new reasoned request and
  certificate.
