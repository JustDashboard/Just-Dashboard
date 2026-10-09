package netx

import (
	"errors"
	"slices"
)

func nativeStructureGuard(p *nativeProfile) error {
	if !slices.Contains([]string{"physical", "dummy", "veth"}, p.View.Kind) || p.View.Contract.Master != "" || len(p.View.Contract.Members) != 0 {
		return errors.New("saved and active native topology must be verified before bond, VRF or member profiles can be activated")
	}
	if p.View.Kind == "veth" {
		return nativeValidatePeer(p.View.Device, p.Device.IfIndex, p.Peer)
	}
	return nil
}
