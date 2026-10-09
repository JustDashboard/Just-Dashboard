package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
)

// ClientDecision is why the browser's replies take ClientPath. Table and
// Device are the kernel's answer to `ip route get CLIENT from SOURCE`. The
// rule is the model's: the kernel reports which table answered, never which
// rule chose it. Basis says how far the two agree:
//
//   - kernel_and_model: the evaluated rule looks up the table the kernel
//     reported and its route leaves by the same device.
//   - kernel: the kernel's table is known; the rule is not (Candidates lists
//     the rules that look that table up).
//   - disagree: the model chose another table or device, so only the
//     kernel's answer is shown.
type ClientDecision struct {
	Basis        string         `json:"basis"`
	Table        int            `json:"table"`
	TableName    string         `json:"tableName,omitempty"`
	Device       string         `json:"device,omitempty"`
	Gateway      string         `json:"gateway,omitempty"`
	RulePriority *int           `json:"rulePriority,omitempty"`
	Candidates   []int          `json:"candidates"`
	Reason       string         `json:"reason,omitempty"`
	Steps        []DecisionStep `json:"steps"`
}

// clientDecision asks the kernel the reply's question with the source it is
// sent from, and evaluates the rules for the same packet: locally generated,
// unmarked, from the sockets answering the client.
func clientDecision(ctx context.Context, view *RoutingView, path Path, vrf bool) *ClientDecision {
	if path.Address == "" || path.Local {
		return nil
	}
	client, err := netip.ParseAddr(path.Address)
	if err != nil {
		return nil
	}
	args := []string{"-j"}
	if client.Is6() {
		args = append(args, "-6")
	}
	args = append(args, "route", "get", client.String())
	if path.Source != "" {
		args = append(args, "from", path.Source)
	}
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return nil
	}
	kernel, err := parseRouteGet(out, Path{Address: client.String()})
	if err != nil || kernel.Local {
		return nil
	}
	byID, byName := rtTables()
	table, name := tableOf(ipTable(kernel.Table), byID, byName)
	if name == "" {
		name = tableName(view, table)
	}
	d := &ClientDecision{
		Basis: "kernel", Table: table, TableName: name, Device: kernel.Device, Gateway: kernel.Gateway,
		Candidates: []int{}, Steps: []DecisionStep{},
	}
	for _, r := range view.Rules {
		if r.Family == familyOf(client) && r.Action == "lookup" && r.Table == table {
			d.Candidates = append(d.Candidates, r.Priority)
		}
	}
	tuple := routeTuple{family: familyOf(client), dst: client, iif: "lo"}
	if src, err := netip.ParseAddr(path.Source); err == nil {
		tuple.src = src
	}
	// One owner among the answering sockets is the reply's UID; several, or
	// none read, leave a UID rule undecidable rather than guessed.
	if uids := replySocketUIDs(ctx, client.String()); len(uids) == 1 {
		tuple.uid = &uids[0]
	}
	model := newModel(view, tuple.family, vrf).decide(tuple)
	d.Steps = model.Steps
	switch {
	case model.Status == "unknown":
		d.Reason = "The kernel reports the table; the rule cannot be named: " + model.Reason
	case model.Status != "route" || model.Table != table || model.Route == nil:
		d.Basis = "disagree"
		d.Reason = fmt.Sprintf("The model reached %s, but the kernel answers from table %s; the kernel's answer is shown.", describeDecision(model), tableLabel(table, name))
	case !routeUses(model.Route, kernel.Device):
		d.Basis = "disagree"
		d.Reason = fmt.Sprintf("The model's route %s does not leave by %s, the kernel's device; the kernel's answer is shown.", model.Route.Destination, kernel.Device)
	case model.RulePriority == nil:
		d.Reason = "The kernel reports the table; " + model.Reason
	default:
		d.Basis, d.RulePriority = "kernel_and_model", model.RulePriority
	}
	return d
}

func tableName(view *RoutingView, id int) string {
	for _, t := range view.Tables {
		if t.ID == id {
			return t.Name
		}
	}
	return ""
}

// readVRFs lists the VRF devices and their tables. A host without the vrf
// module has none, and an unreadable listing is treated as possibly having
// some, so an l3mdev rule stays undecidable instead of ruled out.
func readVRFs(ctx context.Context) ([]VRFDevice, bool) {
	out, err := run(ctx, "ip", "-j", "-d", "link", "show", "type", "vrf")
	if err != nil {
		return []VRFDevice{}, false
	}
	var links []struct {
		Name     string `json:"ifname"`
		LinkInfo struct {
			Kind string `json:"info_kind"`
			Data struct {
				Table json.Number `json:"table"`
			} `json:"info_data"`
		} `json:"linkinfo"`
	}
	if json.Unmarshal([]byte(out), &links) != nil {
		return []VRFDevice{}, false
	}
	vrfs := []VRFDevice{}
	for _, l := range links {
		// iproute2 prints an empty object for every link the type filter
		// excluded.
		if l.Name == "" || l.LinkInfo.Kind != "vrf" {
			continue
		}
		table, _ := strconv.Atoi(l.LinkInfo.Data.Table.String())
		vrfs = append(vrfs, VRFDevice{Name: l.Name, Table: table})
	}
	slices.SortFunc(vrfs, func(a, b VRFDevice) int { return a.Table - b.Table })
	return vrfs, true
}
