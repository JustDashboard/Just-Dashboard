package netx

import "context"

// SQM stays advisory in selected drift repair: changing it requires its owned
// IFB lifecycle, independent recovery and the explicit reconnect protocol.
func driftSQM(ctx context.Context, sh ShapeSpec) DriftObservation {
	o := observation("shaping", sh.Device)
	o.Coverage = "owned-source-root-ifb-cake-redirect"
	v := readShapeVerification(ctx, sh)
	o.Status, o.Reason = v.Status, v.Reason
	if v.Status == "verified" {
		o.Status = "matching"
	}
	if v.Status == "drift" {
		o.Reason += " Review the explicit SQM profile on Traffic; selected drift repair does not own its IFB lifecycle."
	}
	return o
}
