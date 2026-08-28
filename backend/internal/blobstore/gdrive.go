package blobstore

import (
	"context"
	"fmt"
	"io"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// driveFields limits every files.list/files.get call to just the fields
// this store actually reads -- Drive's API returns a large default field
// set otherwise, most of it irrelevant here.
const driveFields = "id, name, mimeType"

// GDriveStore backs Store with a Google Drive folder, admin-configured in
// Settings -> Storage Integration (see StorageConfigService) with a choice
// of two auth methods -- a pasted service-account JSON key, or a
// "Connect your Google account" OAuth flow. Unlike S3Store/GCSStore, Drive
// addresses objects by an opaque file ID, not by Store's flat key string,
// so Put/Get first resolve key -> file ID via a files.list query scoped to
// folderID (update-in-place on Put if found, files.create otherwise) --
// one extra Drive API call per operation versus S3/GCS's direct
// addressing, an accepted tradeoff for the volumes involved here (alert/
// incident evidence attachments, not bulk object storage).
type GDriveStore struct {
	svc      *drive.Service
	folderID string
}

// NewGDriveStoreFromServiceAccount authenticates with an explicit
// service-account JSON key, same pinned-credential-type reasoning as
// GCSStore (option.WithAuthCredentialsJSON(ServiceAccount, ...), not the
// deprecated WithCredentialsJSON, which auto-detects the credential type
// from whatever JSON it's handed -- this admin-supplied blob is only ever
// meant to be a plain service-account key). The service account itself
// must be granted access to folderID from the Drive side (shared with its
// email) -- KuruOps has no way to do that on the admin's behalf.
func NewGDriveStoreFromServiceAccount(ctx context.Context, credentialsJSON, folderID string) (*GDriveStore, error) {
	svc, err := drive.NewService(ctx, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(credentialsJSON)))
	if err != nil {
		return nil, err
	}
	return &GDriveStore{svc: svc, folderID: folderID}, nil
}

// NewGDriveStoreFromOAuth authenticates as whichever Google account the
// admin connected via OAuth (see StorageConfigService.
// HandleGDriveOAuthCallback), using the stored refresh token to mint a
// fresh access token on demand -- oauth2.TokenSource handles that
// transparently on every Drive API call, no background refresh job
// needed. clientID/clientSecret are the app-level Google OAuth client's
// own credentials (config.Config.GoogleOAuthClientID/Secret), required to
// exchange the refresh token -- the same client the "Connect" button's
// authorize URL was built with.
func NewGDriveStoreFromOAuth(ctx context.Context, clientID, clientSecret, refreshToken, folderID string) (*GDriveStore, error) {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{drive.DriveScope},
	}
	ts := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	svc, err := drive.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return &GDriveStore{svc: svc, folderID: folderID}, nil
}

func (s *GDriveStore) Put(ctx context.Context, key string, content io.Reader, _ int64, contentType string) error {
	existingID, err := s.findFileID(ctx, key)
	if err != nil {
		return err
	}
	if existingID != "" {
		_, err := s.svc.Files.Update(existingID, &drive.File{}).Media(content, googleapi.ContentType(contentType)).Context(ctx).Do()
		return err
	}
	file := &drive.File{Name: key, Parents: []string{s.folderID}}
	_, err = s.svc.Files.Create(file).Media(content, googleapi.ContentType(contentType)).Context(ctx).Do()
	return err
}

func (s *GDriveStore) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	fileID, err := s.findFileID(ctx, key)
	if err != nil {
		return nil, "", err
	}
	if fileID == "" {
		return nil, "", ErrNotFound
	}

	file, err := s.svc.Files.Get(fileID).Fields(driveFields).Context(ctx).Do()
	if err != nil {
		return nil, "", err
	}
	contentType := file.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	resp, err := s.svc.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return nil, "", err
	}
	return resp.Body, contentType, nil
}

// findFileID resolves key to the Drive file ID of the (at most one,
// non-trashed) file with that exact name directly inside s.folderID --
// "" if none exists. name/parents equality is exact, not a search index
// match, so this can't accidentally pick up an unrelated file that merely
// contains key as a substring.
func (s *GDriveStore) findFileID(ctx context.Context, key string) (string, error) {
	q := fmt.Sprintf("'%s' in parents and name = '%s' and trashed = false", escapeDriveQueryValue(s.folderID), escapeDriveQueryValue(key))
	list, err := s.svc.Files.List().Q(q).Fields("files(id)").PageSize(1).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if len(list.Files) == 0 {
		return "", nil
	}
	return list.Files[0].Id, nil
}

// escapeDriveQueryValue escapes a value interpolated into a Drive API `q`
// string literal -- Drive's query grammar only requires escaping a
// backslash or single quote inside a quoted string (see Drive API "Search
// for files and folders" docs), so those are the only two characters
// handled here.
func escapeDriveQueryValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return v
}
