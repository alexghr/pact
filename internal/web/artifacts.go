package web

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/state"
)

func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var search state.ArtifactSearch
	search.Query = strings.TrimSpace(query.Get("q"))
	var err error
	if value := query.Get("offset"); value != "" {
		search.Offset, err = strconv.Atoi(value)
		if err != nil {
			http.Error(w, "Invalid artifact offset", http.StatusBadRequest)
			return
		}
	}
	if value := query.Get("creator_pact_session_id"); value != "" {
		search.CreatorPactSessionID, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			http.Error(w, "Invalid creating session", http.StatusBadRequest)
			return
		}
	}
	search.Limit = 50
	page, err := s.store.SearchArtifacts(r.Context(), search)
	if errors.Is(err, state.ErrInvalidArtifact) {
		http.Error(w, "Invalid artifact search", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.serverError(w, r, "list artifacts", err)
		return
	}
	items := make([]artifactListItem, 0, len(page.Artifacts))
	for _, artifact := range page.Artifacts {
		items = append(items, artifactListItem{ArtifactSummary: artifact, URL: artifactURL(artifact.ID)})
	}
	data := pageData{
		Title:                    "Artifacts",
		Artifacts:                items,
		ArtifactCount:            len(items),
		ArtifactQuery:            search.Query,
		ArtifactCreatorSessionID: search.CreatorPactSessionID,
	}
	if page.NextOffset != nil {
		query.Set("offset", strconv.Itoa(*page.NextOffset))
		data.ArtifactNextURL = "/artifacts?" + query.Encode()
	}
	if search.Offset > 0 {
		query.Set("offset", strconv.Itoa(max(0, search.Offset-search.Limit)))
		data.ArtifactPreviousURL = "/artifacts?" + query.Encode()
	}
	s.render(w, r, "artifacts", data)
}

func (s *Server) showArtifact(w http.ResponseWriter, r *http.Request) {
	artifactID, ok := artifactIDFromRequest(w, r)
	if !ok {
		return
	}
	artifact, err := s.store.GetArtifact(r.Context(), artifactID)
	if errors.Is(err, state.ErrArtifactNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "get artifact", err, "artifact_id", artifactID)
		return
	}
	files := make([]artifactFileItem, 0, len(artifact.Files))
	for _, file := range artifact.Files {
		files = append(files, artifactFileItem{
			ArtifactFile: file,
			URL:          artifactFileURL(artifactID, file.Path),
		})
	}
	s.render(w, r, "artifact", pageData{
		Title:         artifact.Name,
		Artifact:      artifact,
		ArtifactFiles: files,
		CreatorURL:    sessionURL(artifact.CreatorPactSessionID),
	})
}

func (s *Server) downloadArtifactFile(w http.ResponseWriter, r *http.Request) {
	artifactID, ok := artifactIDFromRequest(w, r)
	if !ok {
		return
	}
	file, err := s.store.GetArtifactFile(r.Context(), artifactID, r.PathValue("path"))
	if errors.Is(err, state.ErrArtifactFileNotFound) || errors.Is(err, state.ErrInvalidArtifactFile) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "get artifact file", err, "artifact_id", artifactID)
		return
	}
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": path.Base(file.Path),
	}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+file.SHA256+`"`)
	modified, _ := time.Parse(time.RFC3339Nano, file.UpdatedAt)
	http.ServeContent(w, r, path.Base(file.Path), modified, bytes.NewReader(file.Content))
}

func artifactIDFromRequest(w http.ResponseWriter, r *http.Request) (int64, bool) {
	artifactID, err := strconv.ParseInt(r.PathValue("artifactID"), 10, 64)
	if err != nil || artifactID < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return artifactID, true
}

func artifactURL(artifactID int64) string {
	return "/artifacts/" + url.PathEscape(strconv.FormatInt(artifactID, 10))
}

func artifactFileURL(artifactID int64, filePath string) string {
	segments := strings.Split(filePath, "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	return artifactURL(artifactID) + "/files/" + strings.Join(segments, "/")
}
