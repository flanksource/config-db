package matchers

// Matcher defines the contract for any matcher to impletent
type Matcher interface {
	Match(string) ([]string, error)
	Read(string) ([]byte, error)
}
