package httprequest

// Requester defines the contract for making http calls and fetching data
type Requester interface {
	Get(string) ([]byte, error)
}
