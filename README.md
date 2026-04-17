# fins

A Go client library for the Omron FINS protocol.

This is a MaestroHub fork of [`github.com/xiaotushaoxia/fins`](https://github.com/xiaotushaoxia/fins)
(itself derived from `github.com/l1va/gofins`, © 2018 l1va, MIT). See `NOTICE.md`
for the full attribution chain.

## Status

Fork base: upstream `v0.0.2` (commit `dbb9952`).
In-progress improvements are tracked in the MaestroHub fork task doc; the first
tagged release will be `v0.1.0-mh.1` once those changes land.

## Upstream history

The original library supports communication with Omron PLCs from Go. It was
tested against an **Omron PLC NJ501-1300** (~4 ms request-response cycle), and
separately (via the siyka-au repository) against a **CP1L-EM**. A simple Omron
FINS server (PLC emulator) is available in `udpserver.go`.

Ideas in the original implementation came from
[`hiroeorz/omron-fins-go`](https://github.com/hiroeorz/omron-fins-go),
[`patrick--/node-omron-fins`](https://github.com/patrick--/node-omron-fins)
and [`l1va/gofins`](https://github.com/l1va/gofins).

## Remotes

This repo tracks both its fork base and its own history:

```
origin    git@github.com:maestrohub-labs/fins.git     (MaestroHub fork)
upstream  https://github.com/xiaotushaoxia/fins.git   (fork base, read-only)
```

# TODO

1. FINS/TCP transport support (post-V1).
