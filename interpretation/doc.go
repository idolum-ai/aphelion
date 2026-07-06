//go:build linux

// Package interpretation owns Aphelion's central judgment/use service.
//
// Domain packages still interpret their own languages. This package validates
// and records the durable contract around consequential interpretations:
// judgments, uses, effect-attempt commitments, challenges, and decorrelation.
package interpretation
