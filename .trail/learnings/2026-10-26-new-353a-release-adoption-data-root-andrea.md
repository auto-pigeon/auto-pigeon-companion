---
title: "NEW_353A release adoption: data-root fence, macOS peer owner, install identity, EricW 1.4535 pin"
author: Andrea-dintino
date: 2026-10-26
branch: main
cost: { minutes: 30, tokens: 260669 }
touches:
  - internal/web/peer_pcblist.go
  - internal/web/peer_darwin.go
  - internal/web/peer_other.go
  - internal/web/peer_crossuid_test.go
---

## Decided
- aub_task_cli's runtime fence comes from workspace_paths.mapper_root (the one ladder), never MAPPER_ROOT alone; relative/missing MAPPER_ROOT or no established root = refusal; init checks the resolved destination before mkdir.
- macOS loopback-peer owner: sysctl net.inet.tcp.pcblist_n via Go syscall.Sysctl (no cgo). xinpcb_n and xsocket_n are both `#pragma pack(4)`, 104 bytes; uid at xsocket_n+64; lport@18/fport@16 (network order); vflag@44; addresses @48/@64 (IPv4 in the last 4 bytes when INP_IPV4). Compute offsets with gcc from the replicated structs, not by hand.
- AUCOM install identity = binding.origin {listing_id, publisher_id} (store local-binding/1.5); AUB keys listings on (publisher_id, profile_id).
- EricW pin moves: replace AUCOM's release-manifest fixture first (forkrelease_test goes red), then the two fork profiles; re-run AUCOM Q2 leak controls with the NEW_329D runner retargeted; AUT `ericw-tools.sh pin --accept-change --reason`.
## Tried and ruled out
- Matching uidFromPCBList with the server's own tuple: that is the mirror socket (the parser test caught it).
## Watch out
- The Q2 fork profile description is within ~10 bytes of AUCOM's 4000-byte limit.
- `sudo -n` works on ubuntu24 and the CI runners: TestASecondAccountsRealConnectionIsAnotherAccount runs there for real.
