package workspace

// Windows has no Unix filesystem FIFOs; file-type and authority checks still apply.
const nonblockingOpenFlags = 0
