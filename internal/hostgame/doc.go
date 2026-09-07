// Package hostgame is the Companion's half of the community hosted-game
// lifecycle: advertising a server this program is running, keeping the
// advertisement alive while it is, and joining somebody else's.
//
// # Two halves that must not be confused
//
// [Advertiser] is about a process THIS machine is running. It registers a lease
// with AUB after the user has seen what the listing will say, beats while the
// supervised job is alive, and ends the lease with the reason the job actually
// stopped for.
//
// [Joiner] is about somebody else's. It redeems an opaque link, downloads and
// verifies the exact revision being played, checks the engine binding, and hands
// a command preview back for approval. It launches nothing on its own.
//
// # What this package never does
//
// It does not decide whether a server is healthy — the job service already
// supervises the process and this reads its state. It does not talk to any
// address but AUB's — a reachability probe is AUB's, from a machine that is not
// behind the host's own NAT, and a probe from here would establish only that this
// computer can reach itself. It does not discover games from anywhere but AUB,
// and it holds no server list, no master-server client and no cache of other
// people's addresses.
//
// # The three rules an advertisement obeys
//
// **Nothing is registered before the user has read the preview.** [Preview] is
// the same computation the registration performs, run by AUB without writing, and
// [Advertiser.Start] refuses a registration whose `ConfirmExposure` is false
// rather than defaulting it. A game is listed after its host has seen what the
// listing will say, never before.
//
// **A lease is ended by the process, not by the program exiting.** The beat loop
// watches the supervised job, and every way a job can end maps onto a reason AUB
// has a word for: a user cancelling is `host_stopped`, a non-zero exit is
// `host_crashed`, signing out is `owner_signed_out`. Silence is what happens when
// none of those got the chance, and AUB draws its own conclusion from its own
// clock — which is why this program never sends `heartbeat_missed`.
//
// **A restart reclaims; it does not re-register.** [ProcessIdentity] is stable
// across a restart of the same installation hosting the same map on the same
// address, so AUB replaces the lease in place rather than leaving a second
// listing beside one nobody can stop. It is deliberately NOT a process id: a PID
// is different after exactly the event reclaim exists for, and it is readable by
// anything else on the machine.
//
// # The four things a join checks before anything runs
//
// A redeemed link is a description of somebody else's machine, and every part of
// it is checked before a command is offered:
//
//  1. **May this account have the map at all.** AUB answers it in the resolution
//     rather than leaving it to be discovered half way through a download.
//  2. **Are the bytes the ones being played.** The revision is fetched through
//     internal/assetsync, which verifies every file against the digest AUB
//     declared, and the resolution's own `map_content_sha256` is compared as well.
//  3. **Is there an engine for this.** The join is refused by name when no
//     installed profile implements the runtime the host declared, because
//     "connect failed" is a much worse message than "you do not have ironwail".
//  4. **Does the user approve the command.** The preview is the job service's own,
//     which is the argv it would run rather than a re-rendering of it.
package hostgame
