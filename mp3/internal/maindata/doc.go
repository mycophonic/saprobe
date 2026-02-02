// Package maindata is a zero-allocation MPEG1/2 Layer III main data parser.
//
// It replaces the original maindata package, eliminating all per-frame heap
// allocations by using caller-provided destination structs and a persistent
// fixed-size reservoir buffer.
package maindata
