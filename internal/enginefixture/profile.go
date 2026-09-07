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
