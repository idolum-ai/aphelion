//go:build linux

package sandbox

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
)

type Roots struct {
	GlobalRoot        string
	AdminExecRoot     string
	SharedMemoryRoot  string
	UserWorkspaceRoot string
	UserMemoryRoot    string
}

type Scope struct {
	Principal        principal.Principal
	Profile          Profile
	GlobalRoot       string
	SharedMemoryRoot string
	UserWorkspace    string
	UserMemory       string
	WorkingRoot      string
}

type Resolver struct {
	roots    Roots
	profiles Profiles
}

func NewResolver(roots Roots, profiles Profiles) (*Resolver, error) {
	resolvedRoots, err := resolveRoots(roots)
	if err != nil {
		return nil, err
	}
	return &Resolver{
		roots:    resolvedRoots,
		profiles: profiles,
	}, nil
}

func (r *Resolver) Roots() Roots {
	if r == nil {
		return Roots{}
	}
	return r.roots
}

func (r *Resolver) Profiles() Profiles {
	if r == nil {
		return Profiles{}
	}
	return r.profiles
}

func (r *Resolver) Resolve(p principal.Principal) (Scope, error) {
	if r == nil {
		return Scope{}, fmt.Errorf("sandbox resolver is nil")
	}

	profile, err := r.profiles.ForRole(p.Role)
	if err != nil {
		return Scope{}, err
	}

	scope := Scope{
		Principal:        p,
		Profile:          profile,
		GlobalRoot:       r.roots.GlobalRoot,
		SharedMemoryRoot: r.roots.SharedMemoryRoot,
	}

	switch p.Role {
	case principal.RoleAdmin:
		scope.WorkingRoot = r.roots.AdminExecRoot
	case principal.RoleApprovedUser:
		if p.TelegramUserID <= 0 {
			return Scope{}, fmt.Errorf("approved_user principal requires positive telegram user id")
		}
		userKey := strconv.FormatInt(p.TelegramUserID, 10)
		scope.UserWorkspace = filepath.Join(r.roots.UserWorkspaceRoot, userKey)
		scope.UserMemory = filepath.Join(r.roots.UserMemoryRoot, userKey)
		scope.WorkingRoot = scope.UserWorkspace
	default:
		return Scope{}, fmt.Errorf("unsupported role %q", p.Role)
	}

	return scope, nil
}

func resolveRoots(roots Roots) (Roots, error) {
	if strings.TrimSpace(roots.AdminExecRoot) == "" {
		roots.AdminExecRoot = roots.GlobalRoot
	}
	globalRoot, err := resolveRootPath("global_root", roots.GlobalRoot)
	if err != nil {
		return Roots{}, err
	}
	adminExecRoot, err := resolveRootPath("admin_exec_root", roots.AdminExecRoot)
	if err != nil {
		return Roots{}, err
	}
	sharedMemoryRoot, err := resolveRootPath("shared_memory_root", roots.SharedMemoryRoot)
	if err != nil {
		return Roots{}, err
	}
	userWorkspaceRoot, err := resolveRootPath("user_workspace_root", roots.UserWorkspaceRoot)
	if err != nil {
		return Roots{}, err
	}
	userMemoryRoot, err := resolveRootPath("user_memory_root", roots.UserMemoryRoot)
	if err != nil {
		return Roots{}, err
	}
	return Roots{
		GlobalRoot:        globalRoot,
		AdminExecRoot:     adminExecRoot,
		SharedMemoryRoot:  sharedMemoryRoot,
		UserWorkspaceRoot: userWorkspaceRoot,
		UserMemoryRoot:    userMemoryRoot,
	}, nil
}

func DefaultRoots(workspaceRoot, sessionsDBPath string) (Roots, error) {
	workspaceRoot, err := resolveRootPath("workspace_root", workspaceRoot)
	if err != nil {
		return Roots{}, err
	}
	sessionsDBPath, err = resolveRootPath("sessions_db_path", sessionsDBPath)
	if err != nil {
		return Roots{}, err
	}

	stateRoot := filepath.Join(filepath.Dir(sessionsDBPath), "isolated")
	return Roots{
		GlobalRoot:        workspaceRoot,
		AdminExecRoot:     workspaceRoot,
		SharedMemoryRoot:  workspaceRoot,
		UserWorkspaceRoot: filepath.Join(stateRoot, "workspaces"),
		UserMemoryRoot:    filepath.Join(stateRoot, "memory"),
	}, nil
}

func resolveRootPath(name, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}

	path, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	return path, nil
}
