package utils

func IgnoreError[T any](val T, _ error) T {
	return val
}
