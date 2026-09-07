package enginefixture

import _ "embed"

// ProfileJSON is the engine profile that drives the fixture.
//
// It is embedded rather than written out by each test for the same reason the
// program itself is one package: several test packages resolve it, and a
// document copied into three testdata directories is three documents that will
// disagree about an argument the first time one of them is edited.
//
// It is deliberately *not* in internal/profile/builtin. A built-in profile is
// one the shipped Companion offers a user, and offering them a fake engine
// would be offering them something that cannot play Quake.
//
//go:embed fixture.engine.json
var ProfileJSON []byte

// ProfileID is the fixture profile's id.
const ProfileID = "aucom.fixture.q1-engine"

// RecordName is the file the fixture writes its record to, under the profile's
// content root.
const RecordName = "engine-record.json"

// ProfileQ2JSON is the Quake II fixture engine, for the same reason
// [ProfileJSON] is the Quake 1 one.
//
// Its command line after the fixture's own flags is byte-for-byte the built-in
// Yamagi profile's, and `internal/cli`'s acceptance compares the two rather
// than trusting this copy — so a change to one that is not made to the other
// fails a test instead of drifting.
//
//go:embed fixture-q2.engine.json
var ProfileQ2JSON []byte

// ProfileQ2ID is the Quake II fixture profile's id.
const ProfileQ2ID = "aucom.fixture.q2-engine"
