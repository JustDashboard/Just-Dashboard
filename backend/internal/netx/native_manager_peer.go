package netx

import (
	"context"
	"errors"
	"net"
)

// An existing Ethernet profile can own a veth endpoint without owning creation
// of its pair. Both endpoints must remain in the inspected namespace; a name
// or a cross-namespace peer index alone does not establish that relationship.
type nativePeerIdentity struct {
	Device  string `json:"device"`
	IfIndex int    `json:"ifIndex"`
	MAC     string `json:"mac"`
}

func nativePeer(link ipLink, links []ipLink) (*nativePeerIdentity, error) {
	if nativeLinkKind(link) != "veth" {
		return nil, nil
	}
	byName, byIndex := map[string]ipLink{}, map[int]ipLink{}
	for _, item := range links {
		if ValidIfName(item.IfName) != nil || item.IfIndex < 1 || item.IfIndex > 2147483647 {
			return nil, errors.New("native link inventory has unreadable endpoint identities")
		}
		if _, found := byName[item.IfName]; found {
			return nil, errors.New("native link inventory has duplicate endpoint names")
		}
		if _, found := byIndex[item.IfIndex]; found {
			return nil, errors.New("native link inventory has duplicate endpoint indices")
		}
		byName[item.IfName], byIndex[item.IfIndex] = item, item
	}
	observed, found := byName[link.IfName]
	if !found || observed.IfIndex != link.IfIndex || observed.Address != link.Address || observed.Link != link.Link || observed.LinkIndex != link.LinkIndex || nativeLinkKind(observed) != "veth" || len(link.LinkNS) != 0 || len(observed.LinkNS) != 0 {
		return nil, errors.New("native veth endpoint is absent, changed or belongs to another namespace")
	}
	// iproute2 resolves IFLA_LINK to `link` for a known same-namespace
	// endpoint, and otherwise emits `link_index`. Resolve only within this
	// complete unique inventory, rejecting foreign-namespace markers and
	// disagreement between a numeric index and a resolved kernel name.
	resolve := func(endpoint ipLink) (ipLink, bool) {
		if len(endpoint.LinkNS) != 0 {
			return ipLink{}, false
		}
		target, found := byIndex[endpoint.LinkIndex]
		if endpoint.Link != "" {
			named, namedFound := byName[endpoint.Link]
			if !namedFound || found && named.IfIndex != target.IfIndex || endpoint.LinkIndex != 0 && (!found || endpoint.LinkIndex != named.IfIndex) {
				return ipLink{}, false
			}
			target, found = named, true
		}
		return target, found && nativeLinkKind(target) == "veth" && len(target.LinkNS) == 0
	}
	peer, found := resolve(link)
	back, reciprocal := resolve(peer)
	id := &nativePeerIdentity{Device: peer.IfName, IfIndex: peer.IfIndex, MAC: peer.Address}
	if found && reciprocal && back.IfName == link.IfName && back.IfIndex == link.IfIndex && back.Address == link.Address && nativeValidatePeer(link.IfName, link.IfIndex, id) == nil {
		return id, nil
	}
	return nil, errors.New("the existing veth peer is absent, outside this namespace or not reciprocally identified")
}

func nativeValidatePeer(device string, ifindex int, peer *nativePeerIdentity) error {
	if peer == nil || ValidIfName(peer.Device) != nil || peer.Device == device || peer.IfIndex < 1 || peer.IfIndex > 2147483647 || peer.IfIndex == ifindex || len(peer.MAC) != 17 {
		return errors.New("native veth peer identity is unreadable")
	}
	mac, err := net.ParseMAC(peer.MAC)
	if err != nil || len(mac) != 6 {
		return errors.New("native veth peer MAC identity is unreadable")
	}
	return nil
}

func nativePeersEqual(a, b *nativePeerIdentity) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func nativeVerifyPeer(ctx context.Context, u *nativeUndo) error {
	if u.Peer == nil {
		return nil
	}
	links, err := nativeLinks(ctx)
	if err != nil {
		return errors.New("native veth peer inventory could not be verified")
	}
	for _, link := range links {
		if link.IfName == u.Device && link.IfIndex == u.IfIndex && link.Address == u.MAC && nativeLinkKind(link) == u.Kind {
			peer, err := nativePeer(link, links)
			if err == nil && nativePeersEqual(peer, u.Peer) {
				return nil
			}
			break
		}
	}
	return errors.New("native veth endpoint or reciprocal peer changed; its recovery evidence was preserved")
}
