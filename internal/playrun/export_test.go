package playrun

// HoldStoreSaves installs the store's test seam: fn runs before each write and
// an error from it is the write's. Set before any run starts.
func HoldStoreSaves(store *Store, fn func(*Record) error) { store.beforeSave = fn }

// HoldStoreReplace installs the seam at the replace itself: fn runs where the
// finished temporary file is about to be renamed over the record, which is the
// moment a reader must not overlap (NEW_624, #45). Set before any save.
func HoldStoreReplace(store *Store, fn func(*Record)) { store.duringReplace = fn }

// ObserveStoreReads installs the seam at a read: fn runs where Load is about
// to open a record's file. Set before any load.
func ObserveStoreReads(store *Store, fn func()) { store.beforeRead = fn }

// HoldCancelAfterRead installs the seam between Cancel's read of a record and
// what it does about it. Set before Cancel is called.
func HoldCancelAfterRead(service *Service, fn func()) { service.cancelRead = fn }
