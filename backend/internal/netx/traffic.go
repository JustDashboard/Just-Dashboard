package netx

// flowSampler differences each program's TCP byte counters between two reads.
type flowSampler struct{}

func newFlowSampler() *flowSampler { return &flowSampler{} }
