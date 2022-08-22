package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	"net/url"
	"os"
)

func main() {
	dc := flag.String("dc", "", "vSphere datacenter")
	cluster := flag.String("cluster", "", "vSphere cluster")
	vmName := flag.String("vm", "", "VM name")
	evcMode := flag.String("evcmode", "intel-sandybridge", "EVC mode, i.e. intel-sandybridge")
	flag.Parse()

	if *dc == "" || *cluster == "" || *vmName == "" {
		fmt.Println("dc, cluster, and vm are required flags")
		os.Exit(1)
	}

	if os.Getenv("GOVC_PASSWORD") == "" || os.Getenv("GOVC_USERNAME") == "" || os.Getenv("GOVC_URL") == "" {
		fmt.Println("GOVC_PASSWORD, GOVC_USERNAME, and GOVC_URL are required env vars")
		os.Exit(1)
	}

	err := doThings(context.Background(), *dc, *cluster, *evcMode, *vmName)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func doThings(ctx context.Context, dc, cluster, evcMode, vmName string) error {
	c, err := createClient(ctx)
	if err != nil {
		return err
	}

	// find the VM
	finder := find.NewFinder(c.Client)
	dcObj, err := finder.Datacenter(ctx, dc)
	if err != nil {
		return err
	}
	finder.SetDatacenter(dcObj)

	vm, err := finder.VirtualMachine(ctx, vmName)
	if err != nil {
		return err
	}

	// find a cluster
	clusterObj, err := find.NewFinder(c.Client).ClusterComputeResourceOrDefault(ctx, cluster)
	if err != nil {
		return err
	}

	// get it's EVC manager
	evcMgrRes, err := methods.EvcManager(ctx, c.RoundTripper, &types.EvcManager{
		This: clusterObj.Reference(),
	})
	if err != nil {
		return err
	}

	var evcMgr mo.ClusterEVCManager
	pc := property.DefaultCollector(c.Client)
	err = pc.RetrieveOne(ctx, *evcMgrRes.Returnval, nil, &evcMgr)
	if err != nil {
		return err
	}

	// build a list of target EVC mode feature flags
	var masks []types.HostFeatureMask
	for _, e := range evcMgr.EvcState.SupportedEVCMode {
		if e.ElementDescription.Key == evcMode {
			masks = e.FeatureMask
			break
		}
	}
	if len(masks) == 0 {
		return fmt.Errorf("error finding EVC feature masks for %s", evcMode)
	}
	for _, mask := range masks {
		fmt.Println(fmt.Sprintf("mask << VimSdk::Vim::Host::FeatureMask.new({:key => \"%s\", :feature_name => \"%s\", :value => \"%s\"})", mask.Key, mask.FeatureName, mask.Value))
	}

	isComplete := true
	req := types.ApplyEvcModeVM_Task{
		This:          vm.Reference(),
		Mask:          masks,
		CompleteMasks: &isComplete,
	}

	// apply the EVC mode to the VM
	fmt.Printf("Applying EVC mode %s to %s", evcMode, vmName)
	res, err := methods.ApplyEvcModeVM_Task(ctx, c.Client, &req)
	if err != nil {
		return err
	}

	t := object.NewTask(c.Client, res.Returnval)
	return t.Wait(ctx)
}

func createClient(ctx context.Context) (*govmomi.Client, error) {
	u := &url.URL{
		Scheme: "https",
		Host:   os.Getenv("GOVC_URL"),
		Path:   "/sdk",
	}
	u.User = url.UserPassword(os.Getenv("GOVC_USERNAME"), os.Getenv("GOVC_PASSWORD"))

	soapClient := soap.NewClient(u, true)
	vimClient, err := vim25.NewClient(ctx, soapClient)
	if err != nil {
		return nil, fmt.Errorf("could not create new vim25 govmomi client, did you set GOVC_USERNAME, GOVC_PASSWORD, GOVC_URL?: %w", err)
	}
	m := session.NewManager(vimClient)
	err = m.Login(ctx, u.User)
	if err != nil {
		return nil, fmt.Errorf("could not login via vim25 session manager, did you set GOVC_USERNAME, GOVC_PASSWORD, GOVC_URL?: %w", err)
	}

	c := &govmomi.Client{
		Client:         vimClient,
		SessionManager: m,
	}
	return c, nil
}
