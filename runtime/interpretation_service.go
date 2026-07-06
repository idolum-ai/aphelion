//go:build linux

package runtime

import "github.com/idolum-ai/aphelion/interpretation"

func (r *Runtime) interpretationService() interpretation.Service {
	if r == nil {
		return interpretation.Service{}
	}
	if r.interpret != nil {
		return *r.interpret
	}
	return interpretation.NewService(r.store)
}
