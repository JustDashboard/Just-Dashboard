package netx

import (
	"errors"
	"slices"
)

func nativeStructureGuard(p *nativeProfile) error {
	if !slices.Contains([]string{"physical", "dummy"}, p.View.Kind) || p.View.Contract.Master != "" || len(p.View.Contract.Members) != 0 {
		return errors.New("saved and active native topology must be verified before bond, VRF or member profiles can be activated")
	}
	return nil
}
