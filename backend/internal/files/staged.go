package files

// stagePrefix names a transfer that is still arriving: an upload in pieces or
// a fetched link. It sits beside its destination, on its filesystem, until it
// is whole and published under its real name.
const stagePrefix = ".jd-part-"
