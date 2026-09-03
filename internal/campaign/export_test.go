package campaign

// FormatT exposes the arm table's t formatter to the package's external tests.
// The formatter has no state and no inputs beyond the number, so testing it
// directly is cheaper and more exact than provoking a degenerate contrast and
// reading the cell back out of a rendered table.
var FormatT = formatT
