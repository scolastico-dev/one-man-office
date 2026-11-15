package server

import (
	"errors"
	"maps"
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// Secret values of environment variables never leave the server: every
// settings and profile answer passes through hideGeneral or hideProfile, and
// every write body through restoreGeneral or restoreProfile, which put the
// stored value back where the browser sent api.HiddenValue. Field keys of the
// refusals are those of the store's validation errors: "agent_env.FOO",
// "env.FOO", "models.sonnet.env.FOO".

// hideGeneral returns a copy of g whose secret agent environment values are
// api.HiddenValue. The store's maps are never changed.
func hideGeneral(g api.General) api.General {
	g.AgentEnv = settings.HideEnv(g.AgentEnv)
	return g
}

// hideProfile returns a copy of p whose secret values, in its own environment
// and in every model's, are api.HiddenValue. The store's maps are never
// changed.
func hideProfile(p api.Profile) api.Profile {
	p.Env = settings.HideEnv(p.Env)
	if p.Models != nil {
		models := make(map[string]api.ProfileModel, len(p.Models))
		for name, m := range p.Models {
			m.Env = settings.HideEnv(m.Env)
			models[name] = m
		}
		p.Models = models
	}
	return p
}

// hideProfiles applies hideProfile to each profile.
func hideProfiles(ps []api.Profile) []api.Profile {
	out := make([]api.Profile, len(ps))
	for i, p := range ps {
		out[i] = hideProfile(p)
	}
	return out
}

// restoreGeneral returns sent with every hidden agent environment value
// replaced by the stored secret of the same variable. A hidden value without
// a stored secret is refused (code invalid_settings, field agent_env.NAME).
func restoreGeneral(sent, stored api.General) (api.General, error) {
	env, err := settings.RestoreEnv(sent.AgentEnv, stored.AgentEnv, "agent_env")
	if err != nil {
		return api.General{}, fieldError(api.CodeInvalidSettings, err)
	}
	sent.AgentEnv = env
	return sent, nil
}

// restoreProfile returns sent with every hidden value replaced by the stored
// secret of the same variable in the same place: the profile's environment,
// or the environment of the model of the same name. A hidden value without a
// stored secret there (a new or renamed variable, another model) is refused
// (code validation, field env.NAME or models.MODEL.env.NAME). stored is the
// zero Profile when the profile is new.
func restoreProfile(sent, stored api.Profile) (api.Profile, error) {
	env, err := settings.RestoreEnv(sent.Env, stored.Env, "env")
	if err != nil {
		return api.Profile{}, fieldError(api.CodeValidation, err)
	}
	if sent.Env != nil {
		sent.Env = env
	}
	if sent.Models != nil {
		models := make(map[string]api.ProfileModel, len(sent.Models))
		for _, name := range sortedNames(sent.Models) {
			m := sent.Models[name]
			menv, err := settings.RestoreEnv(m.Env, stored.Models[name].Env, "models."+name+".env")
			if err != nil {
				return api.Profile{}, fieldError(api.CodeValidation, err)
			}
			if m.Env != nil {
				m.Env = menv
			}
			models[name] = m
		}
		sent.Models = models
	}
	return sent, nil
}

// fieldError maps a *settings.FieldError to an *api.Error of code with
// Details naming the field.
func fieldError(code string, err error) error {
	var fe *settings.FieldError
	if errors.As(err, &fe) {
		e := api.Errorf(code, "%s", fe.Message)
		e.Details = api.FieldDetail{Field: fe.Field}
		return e
	}
	return err
}

// sortedNames returns the keys of m in order, so the first refusal is always
// the same one.
func sortedNames[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
