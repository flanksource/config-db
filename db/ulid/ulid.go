package ulid

import (
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	v2 "github.com/oklog/ulid/v2"
)

type ULID [16]byte

func (u ULID) AsUUID() string {
	return uuid.UUID(u).String()
}

var pool = sync.Pool{
	New: func() interface{} {
		return v2.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)
	},
}

func New() (ULID, error) {
	entropy := pool.Get()
	result, err := v2.New(v2.Timestamp(time.Now()), entropy.(io.Reader))
	pool.Put(entropy)
	return ULID(result), err
}

func MustNew() ULID {
	entropy := pool.Get()
	defer pool.Put(entropy)
	return ULID(v2.MustNew(v2.Timestamp(time.Now()), entropy.(io.Reader)))
}
