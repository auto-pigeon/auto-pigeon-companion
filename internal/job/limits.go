package job

// What this build keeps, as a number somebody can check a measurement against.
//
// `AUCOM/AUT 229`. The threat model has said since `218` that a tool's output
// is "bounded in memory and on disk" (T19), and the unit tests showed that the
// STORED count stays under head+tail. Neither of those is evidence that the
// Companion's resident memory stays flat while a real child floods a real pipe
// for a quarter of a gigabyte — that is a measurement, it has to be taken on a
// real operating system, and a measurement needs a stated envelope taken from
// the product rather than a threshold whoever wrote the harness felt was
// generous.
//
// So the constants in logbuf.go are published here. A harness that derives its
// bound from [RetentionLimits] cannot drift from what this build actually does:
// changing a constant changes the bound the next run is held to, in the same
// commit, with no second copy of the number to forget.
//
// The two ceilings are deliberately different, and confusing them is the whole
// reason this type has more than one field:
//
//	Kept        what survives the job, on disk and in the record. head+tail.
//	Resident    what the capture may hold WHILE the program is still writing,
//	            which is larger, because the tail is compacted at twice its
//	            bound rather than on every append and because a Go slice's
//	            capacity is not its length.
//
// A measurement compared against Kept would fail on a correct build; one
// compared against nothing at all is what `219` left behind.

// Limits is the bounded-capture envelope for one build of this program.
//
// Every field is bytes unless its name says otherwise, and every one is derived
// from the constants in logbuf.go rather than restated — see [RetentionLimits].
type Limits struct {
	// HeadBytes and TailBytes are the two ends of a stream that are kept.
	HeadBytes int64 `json:"head_bytes"`
	TailBytes int64 `json:"tail_bytes"`
	// KeptBytesPerStream is what a finished stream stores: head+tail. It is the
	// bound `StreamLog.Stored` is checked against.
	KeptBytesPerStream int64 `json:"kept_bytes_per_stream"`
	// MaxLogFileBytes is the raw log on disk: the kept bytes plus the elision
	// marker written between them. One file per stream.
	MaxLogFileBytes int64 `json:"max_log_file_bytes"`

	// ReadChunkBytes is the fixed read buffer. It is why draining a flood does
	// not grow with the flood.
	ReadChunkBytes int64 `json:"read_chunk_bytes"`
	// MaxLineBytes bounds the partial line held between reads.
	MaxLineBytes int64 `json:"max_line_bytes"`
	// MaxDiagnostics bounds the structured findings kept per job.
	MaxDiagnostics int `json:"max_diagnostics"`

	// ResidentBytesPerStream is the worst case a live capture may hold. It
	// allows for the tail growing to twice its bound before compaction, for one
	// read chunk landing on top of that, for a slice's capacity being up to
	// twice its length, and for a partial line.
	ResidentBytesPerStream int64 `json:"resident_bytes_per_stream"`
	// StreamsPerJob is two: stdout and stderr, captured independently.
	StreamsPerJob int `json:"streams_per_job"`
	// ResidentBytesPerJob is StreamsPerJob captures at their worst case.
	ResidentBytesPerJob int64 `json:"resident_bytes_per_job"`
}

// RetentionLimits reports the bounded-capture envelope of this build.
//
// Computed from the constants rather than written out again: a hand-copied
// number is a number that is right until somebody changes the constant, and the
// point of publishing this is that a harness holding the program to it is
// holding it to what it does today.
func RetentionLimits() Limits {
	// The tail is compacted only once it exceeds twice its bound, and the check
	// happens after a chunk has been appended — so this is the largest it can
	// be observed at.
	tailHighWater := int64(2*tailBytes + readChunk)
	// append grows a slice by doubling, so a slice of n bytes may occupy 2n.
	// Counting the capacity rather than the length is what makes this an
	// envelope a measurement can be held to instead of an underestimate that
	// fails on a correct build.
	const growth = 2
	perStream := int64(headBytes) +
		growth*tailHighWater +
		growth*int64(maxLineBytes) +
		int64(readChunk)

	kept := int64(headBytes + tailBytes)
	return Limits{
		HeadBytes:          int64(headBytes),
		TailBytes:          int64(tailBytes),
		KeptBytesPerStream: kept,
		// The marker names two counts and a byte total; 128 is well over its
		// longest rendering and is checked by TestTheElisionMarkerFitsItsBudget.
		MaxLogFileBytes: kept + 128,

		ReadChunkBytes: int64(readChunk),
		MaxLineBytes:   int64(maxLineBytes),
		MaxDiagnostics: maxDiagnostics,

		ResidentBytesPerStream: perStream,
		StreamsPerJob:          2,
		ResidentBytesPerJob:    2 * perStream,
	}
}
