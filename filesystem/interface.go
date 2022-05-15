package filesystem

// Finder defines the contract for any file system finder to impletent
type Finder interface {
	Find(string) ([]string, error)
	Read(string) ([]byte, error)
}
