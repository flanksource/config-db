package processors

import (
	"reflect"
	"testing"
)

func TestSanitizeExtractedAliases(t *testing.T) {
	in := []string{"  foo  ", "", "foo", "bar/", "   ", "baz", "baz"}
	got := sanitizeExtractedAliases(in)
	want := []string{"foo", "baz"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected aliases\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSanitizeExtractedAliases_KeepsSchemeAndInternalDoubleSlash(t *testing.T) {
	in := []string{"aws:///us-east-1/i-123", "kubernetes/cluster/pod/default/nginx"}
	got := sanitizeExtractedAliases(in)

	if !reflect.DeepEqual(got, in) {
		t.Fatalf("unexpected aliases\nwant: %#v\n got: %#v", in, got)
	}
}
