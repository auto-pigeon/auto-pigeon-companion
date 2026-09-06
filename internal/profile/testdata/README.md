# Profile fixtures

Three corpora, and the third is the interesting one.

## `valid/`

The smallest document of each shape that is accepted. `minimal.tool.json` is
the one the README shows, and it is deliberately the *minimum*: every member in
it is required, so a member that stops being required will show up here as a
file that could have been smaller.

## `community/`

Documents written the way a person writes JSON — members in whatever order they
occurred to the author, arguments in both the string and the object spelling —
and describing the same work as the built-in samples in `../builtin/`.

`user-q1-toolchain.tool.json` is one half of
`TestBuiltinAndUserAuthoredResolveToTheSameCommand`. The other half ships inside
the binary. The two documents are different in every way except what they run,
which is the claim the extension model makes and this pair is how it is checked.

`user-q1-toolchain-v2.tool.json` is that profile one version later, asking for
two things it did not ask for before: the network, and write access to the
installed game folder. Neither is visible in a version number. It is the fixture
for the escalation path — the normalized diff, the permission delta, and the
refusal to run under the old grant.

## `malicious/`

Every file here must be refused, and
`TestMaliciousFixturesAreRefusedWithAnActionableMessage` names the sentence a
user should be shown for each one. The second half is the point: a validator
that refuses everything with "invalid profile" is a validator nobody can write
a profile against, and the person who has to act on the message is usually not
the person who wrote the document.

The corpus covers the shapes rather than a list of exploits:

- **shell syntax** — command substitution, chaining, pipes. Inert here, because
  argv never reaches a shell, and refused anyway: a profile containing them was
  written against a shell it will not get, and a reviewer should not have to
  work out for themselves that `; rm -rf` in an argument is harmless.
- **escaping a declared root** — a traversing output path, a traversing
  executable file name, and an option value that would have escaped if text
  options were not restricted to a narrow character set.
- **leaking the author's machine** — an absolute path, a home directory, a
  loopback host, a private address, a JSON Web Token.
- **members that should not exist** — an `install_script`, an AUB record id in a
  portable Game Profile reference, a duplicated member that two parsers would
  read differently.
- **asking for the wrong thing** — `LD_PRELOAD` in an environment allowlist, a
  variable that reads like a credential, a regular expression where a literal
  match belongs, a text option four kilobytes wide.
- **lying to the reviewer** — a bidirectional override that makes an argument
  render differently from how it is stored, and an engine profile that labels a
  dedicated server as a single-player session.
- **structural impossibilities** — an engine action outside the closed
  vocabulary, a pipeline step wired to a later step's output.

A fixture added here without an expectation in that test fails the build, and so
does an expectation left behind after a fixture is deleted.

## `golden/`

The exact canonical bytes a digest is taken over, checked in so a change to the
canonicalizer shows up in a diff rather than only as a changed hash. Regenerate
with:

```console
$ go test ./internal/profile -update
```
