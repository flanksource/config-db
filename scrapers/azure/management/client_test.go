package management

import (
	"github.com/google/uuid"
	"testing"
)

type ClientTests struct {
	token string
}

// Test_Client runs a series of tests to exercise Azure Client behavior from the
// API level.
func Test_Client(t *testing.T) {
	t.Parallel()
	uniqueId, _ := uuid.NewUUID()

	tests := ClientTests{
		token: uniqueId.String(),
	}

	t.Run("ListResourceGroups", tests.listResourceGroups)
	t.Run("listKubernetesClusters", tests.listKubernetesClusters)
	t.Run("listContainerRegistries", tests.listContainerRegistries)
	t.Run("listVirtualMachines", tests.listVirtualMachines)
	t.Run("listLoadBalancers", tests.listLoadBalancers)
	t.Run("listVirtualNetworks", tests.listVirtualNetworks)
	t.Run("listFirewalls", tests.listFirewalls)
	t.Run("listDatabases", tests.listDatabases)
}

func (ct *ClientTests) listResourceGroups(t *testing.T) {
	resourceGroups, err := client.ListResourceGroups(ct.token)
	if err != nil {
		t.Error(err)
	}
	t.Log(resourceGroups)
}
func (ct *ClientTests) listKubernetesClusters(t *testing.T) {
	k8s, err := client.ListKubernetesClusters(ct.token)
	if err != nil {
		t.Error(err)
	}
	t.Log(k8s)
}
func (ct *ClientTests) listContainerRegistries(t *testing.T) {
	reg, err := client.ListContainerRegistries(ct.token)
	if err != nil {
		t.Error(err)
	}
	t.Log(reg)
}
func (ct *ClientTests) listVirtualMachines(t *testing.T) {
	virtual, err := client.ListVirtualMachines(ct.token, "resource_group")
	if err != nil {
		t.Error(err)
	}
	t.Log(virtual)
}
func (ct *ClientTests) listLoadBalancers(t *testing.T) {
	loads, err := client.ListLoadBalancers(ct.token, "resource_group")
	if err != nil {
		t.Error(err)
	}
	t.Log(loads)
}
func (ct *ClientTests) listVirtualNetworks(t *testing.T) {
	vn, err := client.ListVirtualNetworks(ct.token)
	if err != nil {
		t.Error(err)
	}
	t.Log(vn)
}
func (ct *ClientTests) listFirewalls(t *testing.T) {
	fw, err := client.ListFirewalls(ct.token)
	if err != nil {
		t.Error(err)
	}
	t.Log(fw)
}
func (ct *ClientTests) listDatabases(t *testing.T) {
	db, err := client.ListDatabases(ct.token)
	if err != nil {
		t.Error(err)
	}
	t.Log(db)
}
