package file

import "testing"

// test stripPrefix
func TestStripPrefix(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"file://foo", "foo"},
		{"git::foo", "foo"},
		{"git::https://foo", "https://foo"},
		{"foo", "foo"},
		{"", ""},
	}
	for _, c := range cases {
		actual := stripPrefix(c.input)
		if actual != c.expected {
			t.Errorf("stripPrefix(%s) == %s, expected %s", c.input, actual, c.expected)
		}
	}
}

func TestConvertLocalPath(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"file://foo", "foo-ecf5c8ee"},
		{"git::foo", "foo-b943d8a5"},
		{"git::https://foo/path?query=abc", "foo-path-8f49fbdc"},
		{"foo", "foo-acbd18db"},
	}
	for _, c := range cases {
		actual := convertToLocalPath(c.input)
		if actual != c.expected {
			t.Errorf("convertToLocalPath(%s) == %s, expected %s", c.input, actual, c.expected)
		}
	}
}
