# `catalog/` — the acquisition catalogue this project publishes

A catalogue tells the Companion where to download a tool from, how big it is,
what its SHA-256 is, who vouches for it and under what licence. Nothing here is
signed: signing needs a private key, and a private key does not live in a public
repository. What lives here is the **payload** — the part a person reviews in a
pull request — and `companion catalog sign` turns it into the signed document a
machine will accept.

## What is in it

`ericw-tools.catalog.json` publishes two packages.

`ericw-tools.q1` pins the four archives upstream published for ericw-tools
**v0.18.1**, the newest release upstream has not marked a pre-release. Every
size and digest in it was measured by downloading the archive from the URL
beside it. The Linux archive's digest is the same `986531ff…` as the copy AUT
installed and pinned as its compiler oracle in
`auto-pigeon-tools/ericw-tools-contract.json`, which is what ties what the
Companion downloads to what the acceptance gates are measured against.

`ericw-tools.q2` pins the three archives upstream published for
**2.0.0-alpha7**, a pre-release — which is what the Quake II path has, because
Quake II support exists nowhere else. alpha7 rather than the newest alpha
because alpha7 is the build that was unpacked and run while `AUP/AUCOM 215` was
written, and it is where every measured claim in
`internal/profile/builtin/ericw-tools-q2.tool.json` came from. Its Linux digest
is pinned a second time in `internal/catalog/published_test.go`, for the same
reason the Q1 one is.

There is no arm64 entry for either package, on any operating system, and no
32-bit entry for the 2.x line, because upstream published none. The profiles
offer `user_path` there instead, which is the honest answer: a download nobody
published cannot be pinned, and inventing a URL for it would be worse than
saying so.

## Why the file and the id were renamed

It was `ericw-tools-q1.catalog.json`, with `catalog_id: auto-pigeon.q1`. The
Companion fetches **one** catalogue — `catalog.json`, at one configured address
— so a second package cannot live in a second file without making Quake 1 and
Quake II mutually exclusive on a machine. Both packages therefore live in one
document, and a document called `q1` that carries a Quake II compiler is a
document whose name is a lie. The serial ratchet is keyed per id, so the old
id's history is untouched and an old `auto-pigeon.q1` document is still refused
under its own name; no `auto-pigeon.ericw-tools` document has ever been
published, so the new id starts from nothing with nothing to replay.

## Why `signer` is missing from every artifact

A key id is *derived* from the key, so a document kept in Git cannot know the id
of whoever will eventually sign it. An artifact that leaves `signer` out means
"whoever signs this document vouches for this entry", and `companion catalog
sign` fills it in with the signing key's id. With more than one `--key` it
refuses instead, because "several keys signed this, and one of them vouches for
this entry" is a question the publisher has to answer.

## Publishing it

```sh
companion catalog keygen --role anchor  --out anchor.key.json
companion catalog keygen --role catalog --out catalog.key.json
# put the anchor's public entry in anchors.json and the catalogue key's in keyring.json
companion catalog sign --key anchor.key.json  keyring.json     --out keyring.signed.json
companion catalog sign --key catalog.key.json catalog/ericw-tools.catalog.json --out catalog.signed.json
companion catalog verify --anchors anchors.json --keyring keyring.signed.json --catalog catalog.signed.json
```

`serial` goes up by one every time a catalogue is republished, and the machine
that fetched the last one will refuse a lower number. See the README's
"Publishing a catalogue" section for the whole procedure and
`docs/adr/0004-acquisition-is-verified-or-it-does-not-happen.md` for why.

## What it is not

It is not a licence, and downloading through it does not make ericw-tools part
of Auto-Pigeon Companion. ericw-tools is GPL-3.0-or-later as it is distributed
(GPL-2.0-or-later source, linked against Embree, which the shipped README says
makes the binaries GPLv3+), the corresponding source is the `v0.18.1` tag named
in the entry, and the Companion is MIT and separate. `THIRD_PARTY_NOTICES.md`
says the same thing at more length.
