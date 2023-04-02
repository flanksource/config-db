package kube

import (
	"fmt"
	"reflect"

	"github.com/flanksource/commons/console"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Name struct {
	Name, Kind, Namespace string
}

func (n Name) String() string {
	if n.Namespace == "" {
		return fmt.Sprintf("%s/%s/%s", console.Bluef(n.Kind), console.Grayf("*"), console.LightWhitef(n.Name))
	}
	return fmt.Sprintf("%s/%s/%s", console.Bluef(n.Kind), console.Grayf(n.Namespace), console.LightWhitef(n.Name))
}

func (n Name) GetName() string {
	return n.Name
}
func (n Name) GetKind() string {
	return n.Kind
}

func (n Name) GetNamespace() string {
	return n.Namespace
}

func GetName(obj interface{}) Name {
	name := Name{}
	switch obj.(type) {
	case *unstructured.Unstructured:
		object := obj.(*unstructured.Unstructured)
		if object == nil || object.Object == nil {
			return name
		}
		name.Name = object.GetName()
		name.Namespace = object.GetNamespace()
	case metav1.ObjectMetaAccessor:
		object := obj.(metav1.ObjectMetaAccessor).GetObjectMeta()
		name.Name = object.GetName()
		name.Namespace = object.GetNamespace()
	}

	switch obj.(type) {
	case *unstructured.Unstructured:
		object := obj.(*unstructured.Unstructured)
		if object == nil || object.Object == nil {
			return name
		}
		name.Kind = object.GetKind()
	default:
		if t := reflect.TypeOf(obj); t.Kind() == reflect.Ptr {
			name.Kind = t.Elem().Name()
		} else {
			name.Kind = t.Name()
		}
	}

	return name
}
