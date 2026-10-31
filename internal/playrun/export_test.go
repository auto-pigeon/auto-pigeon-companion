package playrun

// HoldStoreSaves installs the store's test seam: fn runs before each write and
// an error from it is the write's. Set before any run starts.
func HoldStoreSaves(store *Store, fn func(*Record) error) { store.beforeSave = fn }

// HoldCancelAfterRead installs the seam between Cancel's read of a record and
// what it does about it. Set before Cancel is called.
func HoldCancelAfterRead(service *Service, fn func()) { service.cancelRead = fn }
