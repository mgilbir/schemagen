//go:build !unix

package external

// makeBlockingFile cannot be built here. Its only caller is a guard that
// already skips this platform, because what that guard watches is the lock in
// internal/testgo deciding an outcome the age check would decide the other way,
// and without a lock there is no such outcome.
func makeBlockingFile(string) bool { return false }
