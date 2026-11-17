package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/folderlist"
)

// folderListTimeout bounds one folder listing of the folder picker.
const folderListTimeout = 5 * time.Second

// getFolders lists the sub-folders of ?path= (the home folder when empty)
// for the folder picker: names and paths only, never files. Names are
// passed as they are; the browser shows them escaped.
func (s *Server) getFolders(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), folderListTimeout)
	defer cancel()
	l, err := folderlist.List(ctx, r.URL.Query().Get("path"), folderlist.MaxEntries)
	if err != nil {
		writeError(w, r, folderError(ctx, err))
		return
	}
	out := api.FolderListing{Path: l.Path, Parent: l.Parent, Home: l.Home, More: l.More, Folders: make([]api.FolderEntry, 0, len(l.Folders))}
	for _, e := range l.Folders {
		out.Folders = append(out.Folders, api.FolderEntry{Name: e.Name, Path: e.Path, Repository: e.Repository, Hidden: e.Hidden, Link: e.Link})
	}
	writeJSON(w, http.StatusOK, out)
}

// folderError maps an error of folderlist.List to its API error.
func folderError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, folderlist.ErrNotAbsolute):
		return api.Errorf(api.CodeValidation, "%v", err)
	case errors.Is(err, folderlist.ErrNotFolder):
		return api.Errorf(api.CodeValidation, "%v", err)
	case errors.Is(err, os.ErrNotExist):
		return api.Errorf(api.CodeNotFound, "%v", err)
	case errors.Is(err, os.ErrPermission):
		return api.Errorf(api.CodeValidation, "%v", err)
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil:
		return api.Errorf(api.CodeUnavailable, "The folder did not answer in time.")
	default:
		return err
	}
}
