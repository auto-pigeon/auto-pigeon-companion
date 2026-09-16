package threat

// rows is the matrix. Read it as a story: a document arrives, it names a
// program, the program is downloaded, an archive is unpacked, a process runs,
// an API is served, a link is clicked, two instances race, a machine crashes,
// a release ships.
//
// Every Evidence entry names a test that exists. [Check] proves that, so a
// renamed or deleted test fails the build rather than leaving a row asserting
// something nothing checks any more.
var rows = []Row{
	// --- a document arrives ------------------------------------------------
	{
		ID: "T01", Category: CatDocument,
		Title:  "A profile document tries to be a command rather than describe one",
		Asset:  "the user's machine",
		Vector: "A shared tool or engine profile whose argument values carry shell metacharacters, a semicolon and a second command, or a placeholder that never resolves and is passed through empty.",
		Mitigation: "There is no shell anywhere in the executor. A document declares an executable and an argument list, and every value reaches argv as one literal element. " +
			"A value that is not its declared type, and a placeholder nothing filled, are refused before anything starts.",
		Evidence: []Evidence{
			{"internal/profile", "TestMaliciousFixturesAreRefusedWithAnActionableMessage"},
			{"internal/profile", "TestValidationReportsEveryFaultAtOnce"},
			{"internal/job", "TestAnInjectionPayloadIsOneLiteralArgvElementAndNeverRuns"},
			{"internal/job", "TestAnUnresolvedPlaceholderIsRefusedRatherThanPassedThrough"},
		},
	},
	{
		ID: "T02", Category: CatDocument,
		Title:      "An update to a granted profile quietly asks for more than was granted",
		Asset:      "the permission the user actually reviewed",
		Vector:     "A community profile is granted at version 1.0. Version 1.1 adds a capability, or 1.0 is republished with different contents under the same version.",
		Mitigation: "A grant is against a document's digest. An update that widens capabilities is an escalation, named in a diff and re-reviewed; a version whose bytes changed is refused outright rather than treated as an update — including one that asks for strictly LESS, because a permission-set comparison is not a check on the bytes anybody read.",
		Evidence: []Evidence{
			{"internal/profile", "TestCapabilityEscalationIsRefusedAndTheDiffNamesIt"},
			{"internal/profile", "TestRepublishingAVersionIsRefusedRatherThanTreatedAsAnUpdate"},
			{"internal/publish", "TestAnUpdateThatAsksForMoreIsReportedAsAnEscalation"},
			{"internal/publish", "TestAVersionThatMovedIsRefusedRatherThanTreatedAsAnUpdate"},
			{"internal/approval", "TestAChangedDocumentInvalidatesTheGrantEvenWhenItAsksForLess"},
			{"internal/cli", "TestAToolProfileSomebodyWroteIsApprovedRunAndWithdrawnFromTheCommandLine"},
		},
	},
	{
		ID: "T03", Category: CatDocument,
		Title:      "Publishing a profile discloses a path, an address or a credential",
		Asset:      "the publisher's own machine and account",
		Vector:     "A user shares the profile they have been using. It carries /home/them, a LAN address, or an option value they pasted a token into.",
		Mitigation: "Export validates and canonicalizes, and canonicalization runs the portability check. A document naming an absolute path, a home directory, a network address or anything credential-shaped cannot be PREVIEWED, let alone published. There is one function that produces publishable bytes.",
		Evidence: []Evidence{
			{"internal/profile", "TestCheckPortableRefusesWhatIsTrueOnOneMachineOnly"},
			{"internal/profile", "TestExportRefusesAnUnportableDocument"},
			{"internal/publish", "TestNothingLocalCanReachAPreview"},
			{"internal/publish", "TestABindingsOwnValuesCannotBePublished"},
			{"internal/binding", "TestATokenCannotEnterAPortableProfile"},
		},
	},
	{
		ID: "T04", Category: CatDocument,
		Title:      "A deployment hands out a trust state this machine never checked",
		Asset:      "the review step before anything unfamiliar runs",
		Vector:     "An AUB deployment marks a community profile `verified` or `builtin`. A client that adopted that word would run an unreviewed document.",
		Mitigation: "An installed profile is `community`, always. `builtin` is the one state authorized with no grant, and it means this build shipped the bytes. The deployment's own badge is carried and shown as information, never as authorization.",
		Evidence: []Evidence{
			{"internal/publish", "TestADeploymentCannotHandOutTrustThisMachineDidNotCheck"},
			{"internal/profile", "TestLocalIsNotTreatedAsVouchedFor"},
			{"internal/profile", "TestBuiltinIsAuthorizedByHavingBeenInstalled"},
			{"internal/publish", "TestNothingIsWrittenWithoutAnApproval"},
		},
	},

	{
		ID: "T50", Category: CatDocument,
		Title:      "One approval surface is weaker than another, or approves something nobody read",
		Asset:      "the review step, on whichever surface the user happens to be on",
		Vector:     "A grant can be recorded from the page and from the command line. Two implementations drift: one requires the digest of the document that was displayed and the other takes whatever is on disk when the request arrives, so the weaker one approves bytes that changed between the review and the decision.",
		Mitigation: "There is one writer of a grant. internal/approval.Service is held by the local API, by `companion toolchain grant` and by the approval half of `engine bind` and the bind route, and it refuses an empty digest and a digest that is not the document on this machine — before writing anything, including the binding. Reviewing is a separate call that writes nothing, so `toolchain grant` with no decision prints the report and exits 2 rather than approving.",
		Evidence: []Evidence{
			{"internal/approval", "TestAnApprovalNamesTheExactDocumentItApproves"},
			{"internal/approval", "TestImportingAndBindingGrantNothing"},
			{"internal/approval", "TestApprovingAPipelineGrantsNothingToTheToolsItRuns"},
			{"internal/cli", "TestTheCommandLineAndThePageRecordTheSameApproval"},
			{"internal/cli", "TestBothSurfacesRefuseTheSameApprovals"},
			{"internal/cli", "TestBindingThroughTheAPIGrantsNothingAndItsApprovalNamesTheDocument"},
		},
	},

	// --- the program is obtained -------------------------------------------
	{
		ID: "T05", Category: CatSupply,
		Title:      "A correctly signed older catalogue is replayed to undo a withdrawal",
		Asset:      "the ability to withdraw a bad or backdoored build",
		Vector:     "Somebody who can serve stale bytes — a cache, a mirror, a network position — answers with yesterday's signed catalogue, in which the revoked artifact is still current.",
		Mitigation: "Serials ratchet, per document id, in state beside config.json rather than in the cache. Revocations are recorded the first time they are seen and are never removed, by any later document or by going offline.",
		Evidence: []Evidence{
			{"internal/catalog", "TestARolledBackSerialIsRefused"},
			{"internal/catalog", "TestARevokedKeyIsRefusedAndStaysRefusedForever"},
			{"internal/catalog", "TestARevokedArtifactIsRefusedAndTheMachineRemembersIt"},
			{"internal/acquire", "TestADowngradedCatalogueIsRefused"},
			{"internal/aue", "TestAReplayedCompatibilityManifestIsRefused"},
		},
	},
	{
		ID: "T06", Category: CatSupply,
		Title:      "The bytes are not the bytes: tampering in transit, or in the cache afterwards",
		Asset:      "every external program this machine runs",
		Vector:     "A modified download; or a cache entry edited after installation, which is the version of this attack that a check-on-install-only design misses entirely.",
		Mitigation: "Size before digest before use, and the digest is checked again on USE, not only on install. A file added to a cache entry is noticed as well as a file changed.",
		Evidence: []Evidence{
			{"internal/acquire", "TestAValidInstallVerifiesBeforeItExtracts"},
			{"internal/acquire", "TestAWrongDigestIsRefusedAndNothingIsInstalled"},
			{"internal/acquire", "TestTamperingIsDetectedOnUseAndNotOnlyOnInstall"},
			{"internal/acquire", "TestAFileAddedToACacheEntryIsDetected"},
			{"internal/aue", "TestAnEditedCacheEntryIsRefused"},
			{"internal/assetsync", "TestTamperedBytesArePublishedNowhere"},
		},
	},
	{
		ID: "T07", Category: CatSupply,
		Title:      "A server sends more than it promised, or a download is cut off half-installed",
		Asset:      "the machine's disk, and the integrity of the cache",
		Vector:     "A response longer than the declared size, filling the disk before a digest can even be computed; or a connection dropped mid-write leaving a partial entry that a later run treats as installed.",
		Mitigation: "The declared size bounds the read and is checked first. Nothing is published into the cache until it has verified, so an interrupted download leaves nothing and the retry succeeds.",
		Evidence: []Evidence{
			{"internal/acquire", "TestAWrongSizeIsRefusedBeforeTheDigestIsEvenComputed"},
			{"internal/acquire", "TestAServerThatSendsMoreThanItPromisedIsRefused"},
			{"internal/acquire", "TestAnInterruptedDownloadLeavesNothingAndTheRetrySucceeds"},
			{"internal/assetsync", "TestATruncatedTransferIsRefused"},
			{"internal/assetsync", "TestAnInterruptedSyncIsCheapToFinishAndReadsNothingHalfDone"},
		},
	},
	{
		ID: "T08", Category: CatSupply,
		Title:      "A verification that could not run is treated as one that passed",
		Asset:      "the whole trust chain",
		Vector:     "No trust anchor configured, an expired document, an unreachable server, a failed signature. Each is a moment where a program that wants to be helpful installs anyway.",
		Mitigation: "Every one of them is a refusal with no path to a download. Offline is a different authority, not a weaker check: it uses only content already verified and recorded, and it says what it cannot do.",
		Evidence: []Evidence{
			{"internal/acquire", "TestNothingFallsBackToAnUnverifiedInstall"},
			{"internal/catalog", "TestAnUnconfiguredTrustAnchorRefusesEverything"},
			{"internal/acquire", "TestAnExpiredCatalogueRefusesToInstallAnything"},
			{"internal/aue", "TestAFailedManagedResolutionDoesNotFallBackToAnything"},
			{"internal/acquire", "TestOfflineUsesOnlyAlreadyVerifiedPinnedContent"},
		},
	},
	{
		ID: "T09", Category: CatSupply,
		Title:      "A compromised backend authorises a download of something else",
		Asset:      "the distinction between who may fetch and what the bytes are",
		Vector:     "The authorization hook rewrites the URL immediately before the fetch. A backend that returned a URL to its own artifact would be choosing what this program runs.",
		Mitigation: "Authorization changes the URL and nothing else. Size, digest and the signature chain are checked exactly as for a public URL, against the catalogue this machine anchors.",
		Evidence: []Evidence{
			{"internal/aue", "TestAnAuthorizedDownloadVerifiesExactlyAsAPublicOneDoes"},
			{"internal/aue", "TestABackendThatWillNotAuthorizeIsARefusalAndInstallsNothing"},
			{"internal/catalog", "TestACatalogueURLMustBeHTTPSAndCarryNoCredentials"},
			{"internal/catalog", "TestAnArtifactAttributedToAKeyThatDidNotSignIsRefused"},
		},
	},
	{
		ID: "T10", Category: CatSupply,
		Title:      "A profile points at a file nothing vouched for, or at one outside the directory it named",
		Asset:      "the executable that actually runs",
		Vector:     "A managed route naming a catalogue package that does not exist; a user-path route whose relative path climbs out of the directory the user chose; a data file presented as a program.",
		Mitigation: "A managed route resolves only through the verified catalogue. A user path is resolved against the chosen directory and refused if it leaves it. A file that is not executable is refused by name.",
		Evidence: []Evidence{
			{"internal/acquire", "TestAProfileCannotNameAFileTheCatalogueNeverVouchedFor"},
			{"internal/acquire", "TestAUserPathCannotReachOutsideTheDirectoryItNames"},
			{"internal/acquire", "TestANonExecutableFileIsRefused"},
			{"internal/acquire", "TestASystemPathResolutionLooksUpOnlyTheCommandsTheOptionNames"},
		},
	},

	// --- an archive is unpacked --------------------------------------------
	{
		ID: "T11", Category: CatArchive,
		Title:      "An archive writes outside the directory it was extracted into",
		Asset:      "every file the user can write",
		Vector:     "A member named `../../.ssh/authorized_keys` or `/etc/passwd`; a directory on the way to a member that is already a symbolic link; a destination name that is already a hard link to something else.",
		Mitigation: "Names are checked before anything is written, the resolved parent is checked against the root again after creation, a first write is O_EXCL, and a REPLACING write renames into place rather than opening the destination — which is the only thing that cannot write through a hard link.",
		Evidence: []Evidence{
			{"internal/pack", "TestExtractRefusesAnArchiveWithATraversalName"},
			{"internal/pack", "TestExtractRefusesToEscapeThroughAPreexistingSymlink"},
			{"internal/pack", "TestReplacingAnExtractedFileDoesNotWriteThroughAHardLink"},
			{"internal/pack", "TestReadPK3RefusesUnsafeNames"},
			{"internal/pack", "TestReadPK3RefusesADirectoryMemberThatEscapes"},
			{"internal/catalog", "TestAnArchivePathThatEscapesOrIsAbsoluteIsRefused"},
			{"internal/assetsync", "TestMaterializeKeepsNestedPathsAndRefusesEscapingOnes"},
		},
	},
	{
		ID: "T12", Category: CatArchive,
		Title:      "An archive expands to more than the machine has",
		Asset:      "the machine's disk and memory",
		Vector:     "A compression bomb, a member count bomb, or a PAK directory whose offsets point far past the end of the file.",
		Mitigation: "A budget on member count, on each member and on the total, applied to what the archive DECLARES before anything is read, and again to what is actually produced.",
		Evidence: []Evidence{
			{"internal/pack", "TestReadPK3RefusesACompressionBomb"},
			{"internal/pack", "TestReadPAKRefusesAnOffsetBomb"},
			{"internal/pack", "TestExtractStopsAtTheBudget"},
			{"internal/pack", "TestReadPK3RefusesTooManyMembersForTheBudget"},
			{"internal/acquire", "TestMaliciousArchivesAreRefusedBeforeAnythingIsPublished"},
		},
	},
	{
		ID: "T13", Category: CatArchive,
		Title:      "A member lies about itself, or the archive is truncated",
		Asset:      "the correctness of everything downstream of an extraction",
		Vector:     "A declared size that does not match the bytes; a name with no terminator; a file cut short.",
		Mitigation: "Every member is read against its declaration and the mismatch is the error, rather than whatever the next reader made of the wrong bytes.",
		Evidence: []Evidence{
			{"internal/pack", "TestReadPK3RefusesAMemberThatLiesAboutItsSize"},
			{"internal/pack", "TestReadPK3RefusesATruncatedArchive"},
			{"internal/pack", "TestVerifyPAKReportsTruncation"},
			{"internal/pack", "TestExtractRefusesAMemberThatDoesNotMatchItsDeclaration"},
			{"internal/pack", "TestReadPAKRefusesAnUnterminatedName"},
		},
	},
	{
		ID: "T14", Category: CatArchive,
		Title:      "Two members that are one file on the user's filesystem",
		Asset:      "which of two files the user ends up with",
		Vector:     "`maps/E1M1.bsp` and `maps/e1m1.bsp` in one archive, on macOS or Windows, where the second silently replaces the first.",
		Mitigation: "Case collisions are detected in the archive and refused, with a case key that does not depend on the machine's locale.",
		Evidence: []Evidence{
			{"internal/pack", "TestExtractRefusesACaseCollision"},
			{"internal/pack", "TestPAKRefusesDuplicateAndCaseCollidingMembers"},
			{"internal/pack", "TestCaseKeyHasNoLocaleInIt"},
		},
	},
	{
		ID: "T15", Category: CatArchive,
		Title:      "An archive delivers something that is not a file, or a file that runs",
		Asset:      "the user's machine",
		Vector:     "A symbolic-link member, a device node, or a member whose stored mode makes it executable.",
		Mitigation: "Only regular files are packed or extracted. Extracted files are 0644 — never the archive's idea of a mode, which for PAK does not exist and for PK3 is the packer's umask.",
		Evidence: []Evidence{
			{"internal/pack", "TestExtractedFilesAreNotExecutable"},
			{"internal/pack", "TestReadPK3RefusesASymlinkMember"},
			{"internal/pack", "TestCollectRefusesSymlinksAndDevices"},
		},
	},

	// --- a process runs ----------------------------------------------------
	{
		ID: "T16", Category: CatExecution,
		Title:      "An option value or a path becomes a second command",
		Asset:      "the user's machine",
		Vector:     "A map filename containing `; rm -rf ~`, an option value with backticks, a path with spaces and metacharacters.",
		Mitigation: "os/exec with an argv, never a command line and never a shell. Every value is one element, and the preview a user approves is built from the same resolution the run uses.",
		Evidence: []Evidence{
			{"internal/job", "TestAnInjectionPayloadIsOneLiteralArgvElementAndNeverRuns"},
			{"internal/profile", "TestOptionValuesReachArgvAsSingleLiteralElements"},
			{"internal/profile", "TestPathsWithSpacesAndMetacharactersArePassedLiterally"},
			{"internal/job", "TestAnOptionValueThatIsNotItsTypeIsRefusedBeforeAnythingRuns"},
			{"internal/engine", "TestAwkwardPathsAndValuesArriveLiterally"},
		},
	},
	{
		ID: "T17", Category: CatExecution,
		Title:      "A tool inherits the user's environment, and with it their credentials",
		Asset:      "every secret in the user's environment: tokens, proxy credentials, LD_PRELOAD",
		Vector:     "os/exec with no Env inherits the parent's. None of that is in the document the user approved: \"run qbsp on my map\" would also mean \"and give it my AWS credentials\".",
		Mitigation: "The environment is constructed from three narrowing sources, none of which is the parent's whole environment. PATH is absent from all three and the validator refuses a document that asks for it.",
		Evidence: []Evidence{
			{"internal/job", "TestTheProcessInheritsNothingItWasNotGiven"},
			{"internal/engine", "TestTheEngineDoesNotInheritTheCompanionsEnvironment"},
			{"internal/profile", "TestAnActionInheritsNothingUnlessItSaysSo"},
		},
	},
	{
		ID: "T18", Category: CatExecution,
		Title:      "A job reads or writes outside the roots it was given",
		Asset:      "the user's files, and the game installation",
		Vector:     "An input path outside the declared roots; an output that is a symbolic link to somewhere else; a request that tries to name its own workspace; staging content into the game's own directories.",
		Mitigation: "Roots are declared and checked, a symlinked output is not published, the workspace path comes from the executor and not from the request, and staging refuses the game's own directories and any name that is a path.",
		Evidence: []Evidence{
			{"internal/job", "TestAnInputOutsideTheDeclaredRootsIsRefused"},
			{"internal/job", "TestASymlinkedOutputIsNotPublished"},
			{"internal/job", "TestTheWorkspacePathCannotBeOverriddenByARequest"},
			{"internal/engine", "TestStageRefusesTheGamesOwnDirectories"},
			{"internal/engine", "TestStageRefusesANameThatIsAPath"},
			{"internal/engine", "TestStageRefusesASymbolicLinkInTheSource"},
		},
	},
	{
		ID: "T19", Category: CatExecution,
		Title:      "A tool never ends, floods, or outlives the program supervising it",
		Asset:      "the machine's disk and memory, and the user's ability to stop things",
		Vector:     "A compiler in an infinite loop; one printing gigabytes; one that forks a child which keeps the pipe open after the parent exits.",
		Mitigation: "A timeout on the WHOLE run rather than per read, a bounded log in memory and on disk, SIGTERM before anything harsher, and a job that does not hang on a grandchild holding the pipe.",
		Evidence: []Evidence{
			{"internal/job", "TestATimeoutStopsTheProgramAndSaysSo"},
			{"internal/job", "TestAnOutputFloodIsBoundedInMemoryAndOnDisk"},
			{"internal/job", "TestAChildThatOutlivesItsParentDoesNotHangTheJob"},
			// AUCOM/AUT 229. The row said "bounded in memory and on disk" and
			// had one flood on one stream to show for it. These are the shapes
			// that flood differently: both streams at once, no newline at all,
			// bursts with the reader idle in between, two noisy jobs sharing a
			// supervisor, and a tree that declines to stop politely.
			{"internal/job", "TestBothStreamsFloodWithoutMixing"},
			{"internal/job", "TestAStreamWithNoNewlinesIsCountedAndStillBounded"},
			{"internal/job", "TestASlowFloodCrossesTheRetentionBoundariesAndStaysTrue"},
			{"internal/job", "TestTwoNoisyJobsKeepSeparateRecordsAndLogs"},
			{"internal/job", "TestAProcessTreeThatIgnoresSIGTERMIsStillStopped"},
			// The bound a measurement is held to has to come from the product.
			{"internal/job", "TestTheLimitsPublishWhatTheCaptureActuallyDoes"},
			{"internal/job", "TestALineCountIsKeptWhenNoRuleAsksForOne"},
			{"internal/aue", "TestAnInvocationThatHangsIsStoppedAndSaysSo"},
			{"internal/aue", "TestOutputPastTheCapIsRefusedRatherThanBuffered"},
			{"internal/aue", "TestCancellationSendsSIGTERMBeforeAnythingHarsher"},
		},
	},
	{
		ID: "T20", Category: CatExecution,
		Title:      "A tool's output steers the reader's terminal",
		Asset:      "the user's ability to believe what they are shown",
		Vector:     "ANSI escapes in a compiler's output: move the cursor, repaint what is above, make a failed build look like one that passed.",
		Mitigation: "The raw log keeps the exact bytes as evidence. The user view is derived from it: valid UTF-8, no control characters but tab, then redaction — in that order, so nothing hides inside an escape sequence.",
		Evidence: []Evidence{
			{"internal/job", "TestTheUserViewRemovesWhatWouldSteerATerminal"},
			{"internal/job", "TestInvalidUTF8SurvivesRawAndIsRepairedForTheReader"},
		},
	},
	{
		ID: "T21", Category: CatExecution,
		Title:      "A subprocess prints half a document and a caller acts on it",
		Asset:      "every decision made from a subprocess's structured output",
		Vector:     "A program exits 0 having printed a warning before its JSON, or half a document, or a document with something appended. Each decodes into a partially filled struct.",
		Mitigation: "The JSON reader refuses an empty body and trailing content, and a run's exit status and stderr are kept rather than replaced by whatever the decode made of it.",
		Evidence: []Evidence{
			{"internal/aue", "TestRunJSONRefusesAnythingThatIsNotOneDocument"},
			{"internal/aue", "TestMalformedProtocolOutputIsRefused"},
			{"internal/aue", "TestACrashKeepsItsExitCodeAndItsStderr"},
		},
	},

	// --- the local API -----------------------------------------------------
	{
		ID: "T22", Category: CatLocalAPI,
		Title:      "A name that resolves to 127.0.0.1 makes a hostile page same-origin",
		Asset:      "the local API, which can start processes",
		Vector:     "`http://rebound.example/` with a DNS record pointing at loopback. The browser treats it as that origin's own server.",
		Mitigation: "The Host header must name a loopback address or `localhost`. A rebound request arrives with `Host: rebound.example` and is refused before anything reads it. The listener binds loopback only.",
		Evidence: []Evidence{
			{"internal/web", "TestAForgedHostIsRefused"},
			{"internal/web", "TestListenBindsLoopbackOnly"},
		},
	},
	{
		ID: "T23", Category: CatLocalAPI,
		Title:      "A page on another origin drives the executor",
		Asset:      "the local API",
		Vector:     "Any page the user has open scripts requests at a guessable local port.",
		Mitigation: "The credential is a header, never a cookie, so there is no ambient credential and no CSRF surface. A cross-origin page cannot set a custom header without a preflight and this server answers none. Origin and Sec-Fetch-Site are checked as well.",
		Evidence: []Evidence{
			{"internal/web", "TestCrossOriginAPIRequestsAreRejected"},
			{"internal/web", "TestAPageOnAnotherSiteIsRefusedEvenWithAToken"},
			{"internal/web", "TestSameOriginAndOriginlessRequestsAreAllowed"},
			{"internal/web", "TestMutatingRoutesRejectGET"},
		},
	},
	{
		ID: "T24", Category: CatLocalAPI,
		Title:      "A route that forgot to be authenticated",
		Asset:      "the local API",
		Vector:     "One handler added without the guard. The failure is invisible until somebody finds it.",
		Mitigation: "The guard is asserted over the route table rather than per handler, so a new route with no guard fails a test. Responses are uncacheable, and there is no filesystem-browsing route at all.",
		Evidence: []Evidence{
			{"internal/web", "TestTheAPINeedsItsToken"},
			{"internal/web", "TestEveryAPIRouteIsGuarded"},
			{"internal/web", "TestAPIResponsesAreNotCached"},
			{"internal/web", "TestThereIsNoFilesystemBrowsingRoute"},
		},
	},
	{
		ID: "T25", Category: CatLocalAPI,
		Title:      "A path segment from a URL is joined onto a filesystem path",
		Asset:      "every file the user can read",
		Vector:     "`GET /api/jobs/..%2f..%2f..%2fetc%2fpasswd/logs`.",
		Mitigation: "A job id is checked against the shape this program mints before it is joined onto anything, in the store rather than at each call site.",
		Evidence: []Evidence{
			{"internal/web", "TestAJobIdFromAURLCannotReachOutsideTheStore"},
			{"internal/job", "TestAJobIdIsRefusedWhenItIsNotOneThisPackageMinted"},
		},
	},
	{
		ID: "T26", Category: CatLocalAPI,
		Title:      "A request or a document carries a member the reader silently ignores",
		Asset:      "the correspondence between what was reviewed and what happens",
		Vector:     "A field added by an attacker, or by a newer publisher, that this build does not know about and therefore does not show to the user — while some other component does act on it.",
		Mitigation: "Strict decoding everywhere a document or a request is read: an unknown member is an error naming its path, not a member dropped.",
		Evidence: []Evidence{
			{"internal/web", "TestUnknownFieldsAreRejected"},
			{"internal/profile", "TestUnknownMemberIsReportedWithItsPath"},
			{"internal/catalog", "TestAnUnknownMemberIsRefused"},
			{"internal/aub", "TestAnUnknownSchemaVersionIsRefused"},
		},
	},

	// --- credentials -------------------------------------------------------
	{
		ID: "T27", Category: CatCredential,
		Title:      "The AUB session token reaches a place a tool or a stranger can read it",
		Asset:      "the user's Auto-Pigeon account",
		Vector:     "A token in an argv, in a child's environment, in a profile document, or in the asset cache beside the files it fetched.",
		Mitigation: "The executor is never given a credential. No token reaches a profile, a request, an argument array or an environment, and nothing in the asset cache carries one. config.json is 0600 and its directory 0700.",
		Evidence: []Evidence{
			{"internal/cli", "TestNothingInTheAssetCacheCarriesTheCredential"},
			{"internal/cli", "TestTheConfigFileHoldingTheTokenIsNotReadableByOthers"},
			{"internal/binding", "TestATokenCannotEnterAPortableProfile"},
			{"internal/job", "TestTheProcessInheritsNothingItWasNotGiven"},
		},
		Residual: &Residual{
			What: "The AUB session token is stored in config.json at 0600, not in the operating system's keychain. " +
				"Any process running as the same user can read it.",
			Why: "Keychain, DPAPI and Secret Service all need cgo or a third-party dependency, and this program has neither — " +
				"which is also what makes its supply chain checkable at all (see T45). The boundary this does not cross was " +
				"already there: a process running as the user can read config.json whatever is in it.",
			Owner:  "andrea-dintino (maintainer)",
			Review: "2027-03-31",
		},
	},
	{
		ID: "T28", Category: CatCredential,
		Title:      "A credential a tool printed ends up in a log attached to a bug report",
		Asset:      "any credential the Companion never held and cannot know about",
		Vector:     "A compiler that echoes its environment, a downloader printing an Authorization header, a URL with userinfo.",
		Mitigation: "Redaction is a safety net over the user view, not the boundary. Literals the program holds are matched exactly; patterns catch what a tool printed that this program never saw. Very short values are not treated as secrets, because redacting them would destroy the log and protect nothing.",
		Evidence: []Evidence{
			{"internal/job", "TestACredentialDoesNotSurviveIntoWhatAUserReads"},
			{"internal/job", "TestAShortValueIsNotTreatedAsASecret"},
		},
	},
	{
		ID: "T29", Category: CatCredential,
		Title:      "A pre-signed URL or a credential in a query string is written into an error or a record",
		Asset:      "a capability that is valid for minutes and is enough to fetch an artifact",
		Vector:     "An authorized download URL carries a signature in its query. An error message quoting the URL, or a record storing it, publishes it.",
		Mitigation: "Errors and records carry a redacted URL — scheme, host and path — through one function, and the catalogue address itself must be HTTPS and carry no credentials.",
		Evidence: []Evidence{
			{"internal/acquire", "TestAnErrorNeverCarriesADownloadQueryString"},
			{"internal/catalog", "TestRedactURLKeepsOnlyWhatAPersonNeeds"},
			{"internal/catalog", "TestACatalogueURLMustBeHTTPSAndCarryNoCredentials"},
		},
	},
	{
		ID: "T30", Category: CatCredential,
		Title:      "A compatibility report sends more than the person chose to send",
		Asset:      "the user's own maps, logs and paths",
		Vector:     "A \"help us fix this\" form that quietly attaches the build log, which contains the path to their home directory and whatever the tool printed.",
		Mitigation: "Consent is four booleans defaulting to none, and the report records what was chosen so \"did not share\" and \"shared, and there was none\" stay distinguishable. There is no member that can hold a map, a log or a path. A credential or a path is REFUSED and named, not quietly redacted — a user must not be handed a document they believe they wrote.",
		Evidence: []Evidence{
			{"internal/feedback", "TestNothingIsAttachedWithoutConsent"},
			{"internal/feedback", "TestAReportCarryingASecretAPathOrAnAddressIsRefused"},
			{"internal/feedback", "TestADiagnosticCannotCarryTheToolsOutput"},
			{"internal/feedback", "TestTheReportHasNowhereToPutAFile"},
			{"internal/web", "TestTheReportRouteRefusesAPathInsteadOfScrubbingIt"},
		},
	},

	// --- deep links --------------------------------------------------------
	{
		ID: "T31", Category: CatDeepLink,
		Title:      "A link that is not a join link is followed anyway",
		Asset:      "which server this machine is told to contact",
		Vector:     "`https://evil.invalid/join/x`, which looks similar enough that a lenient parser follows it — and its host is somebody else's choice.",
		Mitigation: "One parser, accepting this scheme and the bare opaque id and nothing else. A query string, a fragment or another scheme is refused with a message showing what a link looks like.",
		Evidence: []Evidence{
			{"internal/hostgame", "TestOnlyTheSchemeAUBPublishedIsFollowed"},
			{"internal/urischeme", "TestTheSchemeIsTheOneTheParserAccepts"},
		},
	},
	{
		ID: "T32", Category: CatDeepLink,
		Title:      "A registered handler launches something the moment a link is clicked",
		Asset:      "the user's machine, from any web page that can emit a link",
		Vector:     "A scheme handler registered as `companion game join %u --approve`, or one that goes through a shell, or one where the URL can become two arguments.",
		Mitigation: "The registered command is `game open` (244F), which has no approval flag: it checks the link's shape before anything else, records it for the Companion's page and raises that page, and starts nothing; the page redeems the link once and a join still takes a fresh review and an approval. The URL is one argv element through a field code, quoted on Windows, with no shell on any platform.",
		Evidence: []Evidence{
			{"internal/urischeme", "TestTheRegisteredCommandOnlyEverShowsAPlan"},
			{"internal/urischeme", "TestTheURLArrivesAsOneArgumentOnEveryPlatform"},
			{"internal/urischeme", "TestWindowsRegistrationIsPerUserAndQuotesBothTheBinaryAndTheURL"},
			{"internal/hostgame", "TestNothingLaunchesWithoutApproval"},
			{"cmd/companion", "TestEveryPackageRegistersTheHandlerTheSameWay"},
			{"internal/joinintent", "TestALinkIsRecordedNotRedeemedAndRedeemedOnlyOnce"},
			{"internal/joinintent", "TestATicketNeverOutlivesItsLifetime"},
			{"internal/web", "TestAPendingLinkIsRedeemedOnceAndStartsNothing"},
		},
	},
	{
		ID: "T33", Category: CatDeepLink,
		Title:      "A handler survives the program it points at, or cannot be removed",
		Asset:      "the user's desktop configuration",
		Vector:     "An uninstall that leaves `autopigeon://` pointing at a deleted binary; or a registration that rewrote somebody's mimeapps.list and cannot put it back.",
		Mitigation: "The executable is resolved and checked before anything is written. Unregistering removes exactly what registering wrote and leaves every other association and section alone. The Windows installer's key carries uninsdeletekey, so an uninstall takes the handler with it.",
		Evidence: []Evidence{
			{"internal/urischeme", "TestAHandlerIsNeverPointedAtAnExecutableThatIsNotThere"},
			{"internal/urischeme", "TestUnregisteringLeavesEverySomebodyElsesAssociationAlone"},
			{"internal/urischeme", "TestWindowsUnregisteringDeletesOnlyThisSchemesKey"},
			{"cmd/companion", "TestUninstallingRemovesTheHandlerOnEveryPackagedPlatform"},
		},
	},
	{
		ID: "T34", Category: CatDeepLink,
		Title:      "A forged hosted game sends a player somewhere, or hands them a different map",
		Asset:      "what the player connects to and what they run",
		Vector:     "A resolution naming a map this account may not read, join-content files that are not the package the game names, a manifest that stages outside the game directory, a game that changed between setup and launch, or an engine the machine does not have.",
		Mitigation: "One readiness model (internal/joinready) decides, in order: the game is live and joinable, there is an engine for its runtime (the host's runtime never picks an executable), the owned program and game folder are bound, and the join content is readable, re-checked against AUB's manifest rules on this machine, verified file by file through assetsync against the package digest the lease names, and staged atomically into a managed base directory that only links the owned game. Ready is reported only after the job service previewed the command. A fresh ticket is spent only immediately before the review, and the revision, package and endpoint are revalidated against the setup; the person then approves the exact command, once.",
		Evidence: []Evidence{
			{"internal/hostgame", "TestABytesMismatchIsRefused"},
			{"internal/hostgame", "TestAJoinIsRefusedBeforeADownloadWhenTheMapIsNotThisAccountsToHave"},
			{"internal/hostgame", "TestAMissingEngineIsRefusedByNameAndBeforeTheDownload"},
			{"internal/hostgame", "TestAPortIsNeverInvented"},
			{"internal/hostgame", "TestAUBsWarningsReachThePerson"},
			{"internal/hostgame", "TestAGameThatChangedDuringSetupIsRefusedAfterTheFreshTicket"},
			{"internal/hostgame", "TestSetupNeverSpendsATicket"},
			{"internal/hostgame", "TestTwoTabsApprovingAtOnceStartOneGame"},
			{"internal/joincontent", "TestThisMachineRefusesWhatAUBRefusesEvenIfAUBServedIt"},
			{"internal/joincontent", "TestATamperedServedFileNeverReachesTheStage"},
			{"internal/joincontent", "TestTheOverlayLinksTheOwnedGameAndWritesNothingIntoIt"},
			{"internal/joinready", "TestReadyIsOnlyReportedAfterTheServicePreviewedTheCommand"},
		},
		Residual: &Residual{
			What: "The server host and port a join connects to come from AUB's resolution and are not independently verified. " +
				"A compromised deployment can point a player's engine at any address.",
			Why: "There is nothing for this machine to check them against: the endpoint is the deployment's own runtime fact, " +
				"and AUB is the authority for it. What IS checked is everything that decides what RUNS — the map bytes against " +
				"two independent digests, the engine profile against its grant, and the whole command against the person's approval. " +
				"An endpoint is where a game connects, not what it executes.",
			Owner:  "andrea-dintino (maintainer)",
			Review: "2027-03-31",
		},
	},

	// --- two instances -----------------------------------------------------
	{
		ID: "T35", Category: CatRace,
		Title:      "Two instances write the same local state and one change disappears",
		Asset:      "the catalogue trust state, which is where revocations live, and the binding store, which is where approvals live",
		Vector:     "The GUI server and a `companion` invocation in a terminal both read catalog-state.json, each records a different revocation, both write. Atomic writes make both succeed and one revocation is gone, with no error anywhere. The same shape over bindings.json costs an approval, or resurrects one somebody withdrew.",
		Mitigation: "A cross-process lock, and for the trust state a monotone merge under it: serials take the max, revocations take the union. The window between reading that file and writing it back spans a network fetch, so a lock alone would not have been enough. Every write to the binding store goes through binding.Update, which reads inside the same lock.",
		Evidence: []Evidence{
			{"internal/lockfile", "TestOnlyOneWriterIsEverInsideTheCriticalSection"},
			{"internal/approval", "TestConcurrentGrantsAndWithdrawalsLoseNoUnrelatedBinding"},
			{"internal/config", "TestAChangeByAnotherInstanceIsNotUndoneByThisOne"},
			{"internal/config", "TestConcurrentUpdatesAllLand"},
			{"internal/catalog", "TestARevocationRecordedByAnotherInstanceIsNotOverwritten"},
			{"internal/catalog", "TestConcurrentWritersLoseNoRevocation"},
			{"internal/catalog", "TestAnOlderSerialCannotBeWrittenBackOverANewerOne"},
		},
	},
	{
		ID: "T36", Category: CatRace,
		Title:      "A lock left behind by a killed process makes the program unusable, or is broken too eagerly",
		Asset:      "the ability to write local state at all",
		Vector:     "An O_EXCL lock is not released by the kernel when its holder dies. Break it too eagerly and two writers proceed; never break it and one kill -9 wedges the program.",
		Mitigation: "The holder refreshes the lock's modification time; a lock nobody has refreshed for a minute is broken THROUGH A RENAME, so of two processes that both judged it abandoned exactly one proceeds. A holder whose lock was broken says so and deletes nothing.",
		Evidence: []Evidence{
			{"internal/lockfile", "TestAnAbandonedLockIsBrokenAndTheTakeoverIsReported"},
			{"internal/lockfile", "TestALiveLockIsRefreshedAndNeverJudgedAbandoned"},
			{"internal/lockfile", "TestAHolderWhoseLockWasBrokenSaysSoAndDeletesNothing"},
			{"internal/lockfile", "TestWithReleasesTheLockWhenTheBodyFails"},
		},
	},
	{
		ID: "T37", Category: CatRace,
		Title:      "Concurrent jobs share a workspace, collide over artifacts, or leak processes",
		Asset:      "the correctness of every build running at once",
		Vector:     "Two builds of the same map writing into one directory; two jobs publishing an artifact of the same name; a soak that leaves goroutines or processes behind.",
		Mitigation: "One directory per job, artifacts namespaced by job, concurrency bounded, and installs of one artifact deduplicated into a single cache entry.",
		Evidence: []Evidence{
			{"internal/job", "TestConcurrentJobsAreBoundedAndDoNotShareAWorkspace"},
			{"internal/job", "TestArtifactsOfDifferentJobsDoNotCollide"},
			{"internal/job", "TestNoGoroutinesOrProcessesSurviveASoak"},
			{"internal/acquire", "TestConcurrentInstallsOfTheSameArtifactProduceOneEntry"},
			{"internal/pathpick", "TestSecondDialogIsRefusedWhileOneIsOpen"},
		},
	},

	// --- the machine misbehaves --------------------------------------------
	{
		ID: "T38", Category: CatRecovery,
		Title:      "A crash leaves a job saying `running` with nothing running",
		Asset:      "the user's trust in what the program says, and their unfinished work",
		Vector:     "A laptop lid, a power cut, a kill -9 during a build. The record says running; nothing is. The WRONG repair is re-running whatever was unfinished, which is how one interrupted publish becomes two.",
		Mitigation: "Recovery changes the record to say the true thing — interrupted, meaning nobody knows how this ended — and never re-runs it. Running it again is a new job the user asks for. A heartbeat, not a pid, distinguishes a crashed job from one another process is supervising right now.",
		Evidence: []Evidence{
			{"internal/job", "TestARestartMarksAnAbandonedJobInterruptedAndNeverRerunsIt"},
			{"internal/job", "TestRecoveryLeavesAJobAnotherProcessIsStillSupervising"},
			{"internal/job", "TestTheHeartbeatFollowsThisProcessesOwnJobsOnly"},
			{"internal/web", "TestRestartPreservesStateAndRunsNothingTwice"},
			{"internal/job", "TestAQueuedJobIsNotStartedByAShutdown"},
		},
	},
	{
		ID: "T39", Category: CatRecovery,
		Title:      "A state file is corrupt, and reading it as empty resets a security decision",
		Asset:      "the serial ratchet and the sticky revocations",
		Vector:     "A file truncated by a crash or a full disk. A program that treats an unreadable ratchet as an absent one has been talked into exactly the state a replay needs.",
		Mitigation: "A missing file is empty state and no error; a file that exists and cannot be read is an error. A corrupt trust state fails a WRITE too, rather than being replaced with a fresh one.",
		Evidence: []Evidence{
			{"internal/catalog", "TestAMissingTrustStateIsEmptyAndACorruptOneIsAnError"},
			{"internal/catalog", "TestACorruptStateFileIsNotSilentlyReplaced"},
			{"internal/config", "TestLoadFromMalformedFileIsAnError"},
			{"internal/config", "TestMigrateRefusesAMalformedFile"},
			{"internal/assetsync", "TestVerifyCatchesACacheThatLostOrChangedBytes"},
		},
	},
	{
		ID: "T40", Category: CatRecovery,
		Title:      "A full disk or a read-only directory leaves half of something behind",
		Asset:      "every file this program writes",
		Vector:     "A write that fails partway. A config file truncated to nothing takes the stored session with it; an archive half-written looks like a package.",
		Mitigation: "Every mutable file is written through a temporary file and a rename, so a failed write leaves the previous content. An unwritable directory is reported by name BEFORE anything is read into memory and half-changed.",
		Evidence: []Evidence{
			{"internal/config", "TestAnUnwritableConfigDirectoryIsReportedAndChangesNothing"},
			{"internal/lockfile", "TestAnUnwritableDirectoryIsRefusedBeforeAnythingIsRead"},
			{"internal/pack", "TestCreateLeavesNothingBehindWhenItFails"},
			{"internal/pack", "TestAFailedReplacementLeavesTheOriginalFileIntact"},
			{"internal/engine", "TestAFailedStagingLeavesNothingBehind"},
			{"internal/config", "TestAMutationThatFailsWritesNothing"},
		},
	},
	{
		ID: "T41", Category: CatRecovery,
		Title:      "A path with non-ASCII characters, or one longer than the platform allows",
		Asset:      "whether the program works at all for people whose names are not ASCII",
		Vector:     "A home directory called `C:\\Users\\Zoë`; a map under a deeply nested project; an archive member whose name is longer than the format's field, or one carrying a NUL that ends it early for an engine reading a C string.",
		Mitigation: "Two answers for two different places, and the difference is deliberate. A FILESYSTEM path is somebody's home directory and must work: non-ASCII round-trips through the config file and reaches argv literally. An ARCHIVE member path becomes a path on a stranger's machine and is printable ASCII only — a PAK name field declares no encoding, ZIP has two and a flag that is often wrong, and case-collision detection is exactly correct on ASCII and merely plausible on Unicode. Anything else is REFUSED, naming the byte and its offset, never transliterated into a name the author did not choose.",
		Evidence: []Evidence{
			{"internal/pack", "TestCheckEntryPathLength"},
			{"internal/pack", "TestCaseKeyHasNoLocaleInIt"},
			{"internal/pack", "TestTheCaseKeyCannotBeReachedByALocaleDependentCharacter"},
			{"internal/pack", "TestAnOverlongUnicodeNameIsRefusedRatherThanTruncated"},
			{"internal/pack", "TestANonASCIIMemberNameIsRefusedByNameAndNotTranslated"},
			{"internal/pack", "TestANULInAMemberNameIsCalledOutSeparately"},
			{"internal/config", "TestANonASCIIPathRoundTripsThroughTheConfigFile"},
			{"internal/config", "TestADeeplyNestedConfigPathEitherWorksOrSaysWhy"},
			{"internal/engine", "TestAwkwardPathsAndValuesArriveLiterally"},
		},
	},
	{
		ID: "T42", Category: CatRecovery,
		Title:      "There is no native file dialog on this machine",
		Asset:      "whether the GUI is usable on a headless or minimal desktop",
		Vector:     "A CI runner, a minimal window manager, a machine with no zenity and no kdialog. A program that assumed one shows a blank page.",
		Mitigation: "The absence is its own error value, and the page falls back to its text fields. A helper that fails, or returns something that is not a directory, is refused rather than used.",
		Evidence: []Evidence{
			{"internal/pathpick", "TestPickWithoutAHelperReportsErrNoHelper"},
			{"internal/pathpick", "TestPickRefusesWhatAHelperReturns"},
			{"internal/pathpick", "TestPickReportsAHelperThatFailed"},
			{"internal/pathpick", "TestCancellation"},
		},
	},
	{
		ID: "T43", Category: CatRecovery,
		Title:      "The network goes away mid-operation, or the machine sleeps and wakes elsewhere",
		Asset:      "the ability to keep working, and the integrity of what was half-done",
		Vector:     "A laptop suspended during a sync; a VPN that changes which hosts resolve; an offline machine asked to build.",
		Mitigation: "An interrupted sync is cheap to finish and reads nothing half-done. Offline uses only already-verified pinned content, still checks the protocol handshake, and says what it cannot do rather than doing less of it.",
		Evidence: []Evidence{
			{"internal/acquire", "TestOfflineUsesOnlyAlreadyVerifiedPinnedContent"},
			{"internal/aue", "TestOfflineRunsTheCachedBuildAndStillChecksTheProtocol"},
			{"internal/aue", "TestOfflineWithNoRecordedRequirementRefuses"},
			{"internal/assetsync", "TestResolvingCurrentOfflineSaysWhatIsMissing"},
			{"internal/assetsync", "TestAnInterruptedSyncIsCheapToFinishAndReadsNothingHalfDone"},
		},
	},
	{
		ID: "T44", Category: CatRecovery,
		Title:      "Antivirus holds or quarantines a freshly downloaded executable",
		Asset:      "the first run on a Windows machine",
		Vector:     "Defender or a third-party scanner opens a newly written .exe, and the rename or the exec fails with a sharing violation for a second or two — or the file is quarantined and never appears.",
		Mitigation: "Nothing is published into the cache until it has verified, so a failure at this point leaves no partial entry and the retry succeeds. A missing executable is reported by name with what to do about it, rather than as a generic exec failure.",
		Evidence: []Evidence{
			{"internal/acquire", "TestAnInterruptedDownloadLeavesNothingAndTheRetrySucceeds"},
			{"internal/job", "TestAMissingExecutableSaysWhatToDoAboutIt"},
		},
		Manual: "On a Windows machine with Defender real-time protection ON: `companion acquire install --package auto-pigeon.ericw-tools.q1`, " +
			"then immediately `companion build run --pipeline auto-pigeon.pipeline.q1-fast-preview --map <a map>`. " +
			"Expected: either it works, or it fails naming the executable and the retry succeeds. " +
			"Not expected: a half-installed cache entry that a later run treats as installed, or an error that does not name a file.",
		Residual: &Residual{
			What: "No automated test exercises a real antivirus scanner holding a file open. The evidence above is the " +
				"structural property — nothing partial is ever published — not an observation of Defender.",
			Why: "A scanner cannot be driven from CI, and a fake one that returns a sharing violation would be testing the " +
				"fake. The manual procedure above is the observation, run before a Windows release.",
			Owner:  "andrea-dintino (maintainer)",
			Review: "2027-03-31",
		},
	},

	// --- the release -------------------------------------------------------
	{
		ID: "T45", Category: CatRelease,
		Title:      "The artifact contains something it says it does not",
		Asset:      "the licence of everything shipped, and the user's ability to know what they are running",
		Vector:     "An extractor staged back into the binary with go:embed; a Go dependency added without anybody deciding; a game asset packaged into somebody's PK3.",
		Mitigation: "The module graph is read out of the BINARY, not out of go.mod, and it is empty. CI fails on a staged extractor, on a go:embed directive in that package, and on any committed executable anywhere in the tree. The packaging policy holds unknown content for review rather than including it.",
		Evidence: []Evidence{
			{"internal/release", "TestThisProgramLinksNoExternalModule"},
			{"internal/release", "TestOnlyTheCompanionIsInTheArtifact"},
			{"cmd/companion", "TestNoticesCoverEveryRedistributedComponent"},
			{"internal/pack", "TestPolicyRefusesAKnownAssetByContent"},
			{"internal/pack", "TestPolicyHoldsWhatItKnowsNothingAbout"},
		},
	},
	{
		ID: "T46", Category: CatRelease,
		Title:      "A copyleft binary is handed out with no offer of its source",
		Asset:      "compliance with the licences of the programs this project distributes",
		Vector:     "A catalogue entry for a GPL tool published without a corresponding-source URL, or a component list that quietly omits one.",
		Mitigation: "The release composer refuses a copyleft package offering no corresponding source, before anything can sign it. The component list is derived from the built-in profiles, so a toolchain added without a licence cannot become invisible.",
		Evidence: []Evidence{
			{"internal/release", "TestEveryDownloadedCopyleftComponentOffersItsSource"},
			{"internal/catalog", "TestAReleaseWithNoCorrespondingSourceIsRefusedBeforeSigning"},
			{"internal/catalog", "TestTheGPLNoticeSaysWhatIsActuallyBeingConveyed"},
			{"internal/cli", "TestCatalogReleaseRefusesAReleaseWithNoCorrespondingSource"},
		},
	},
	{
		ID: "T47", Category: CatRelease,
		Title:      "A release nobody can check the bytes of",
		Asset:      "a downloader's ability to tell the published artifact from a substituted one",
		Vector:     "No checksums, or checksums generated separately from the artifacts and out of step with them.",
		Mitigation: "Checksums and the SBOM are generated FROM the built directory and the built binary. The SBOM is byte-identical between two runs of one build, so it can be published beside a digest of itself.",
		Evidence: []Evidence{
			{"internal/release", "TestChecksumsDigestEveryReleasedFileAndNotItself"},
			{"internal/release", "TestTheSBOMIsValidJSONAndNamesEveryExternalProgram"},
			{"internal/release", "TestTheSBOMIsByteIdenticalWithoutATimestamp"},
			{"cmd/companion", "TestTheReleaseScriptProducesChecksumsAndAnSBOM"},
		},
	},
	{
		ID: "T48", Category: CatRelease,
		Title:      "An unsigned artifact, or a CI job that could sign one from a pull request",
		Asset:      "everybody who installs this program",
		Vector:     "No code signing certificate exists, so Gatekeeper and SmartScreen warn. The dangerous repair is a signing key in the workflow that every pull request runs.",
		Mitigation: "The verification workflow reads no secret, and a job of its own fails the moment one is added. The packaging says plainly that it is unsigned rather than implying otherwise, and the signing steps are written down as the procedure they will be.",
		Evidence: []Evidence{
			{"cmd/companion", "TestCIBuildsEverySupportedTarget"},
			{"cmd/companion", "TestThePackagingIsTruthfulAboutNotBeingSigned"},
		},
		Residual: &Residual{
			What: "Released artifacts are unsigned and un-notarized. macOS shows a Gatekeeper warning and Windows shows " +
				"SmartScreen on first run, and a user has no cryptographic way to tell a published build from a substituted one " +
				"beyond the published SHA256SUMS.",
			Why: "No Apple Developer ID and no Authenticode certificate exist for this project. Both cost money and identity " +
				"verification, and neither can be obtained by writing code. What exists instead is stated truthfully everywhere " +
				"it matters, and the procedures are written down so the day a certificate exists is a day of running them.",
			Owner:  "andrea-dintino (maintainer)",
			Review: "2027-03-31",
		},
	},
	{
		ID: "T49", Category: CatRelease,
		Title:      "An uninstall takes the user's work with it, or leaves the machine changed",
		Asset:      "build history, granted profiles, the catalogue ratchet, and downloaded third-party binaries",
		Vector:     "A package script that deletes ~/.config and ~/.cache; or one that leaves an autopigeon:// handler pointing at a deleted binary.",
		Mitigation: "Package scripts touch nothing under any user's home: they run as root and cannot know whose files those are. Removing user data is a per-user command the person runs themselves, which says exactly what it will delete and requires confirmation. The Windows handler key carries uninsdeletekey and the Linux one is a file the package owns.",
		Evidence: []Evidence{
			{"cmd/companion", "TestNoPackageScriptTouchesUserData"},
			{"cmd/companion", "TestUninstallingRemovesTheHandlerOnEveryPackagedPlatform"},
			{"internal/cli", "TestUninstallShowsWhatItWouldDeleteAndDeletesNothingWithoutConfirmation"},
			{"internal/cli", "TestUninstallPurgeRemovesOnlyThisProgramsDirectories"},
		},
	},
}
