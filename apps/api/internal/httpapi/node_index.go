package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/internal/nodeindex"
)

var errInvalidNodePackageQuery = errors.New("invalid node package query")
var nodePackageCategoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (handler *handler) getNodeIndexStatus(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, handler.dependencies.NodePackages.Status())
}

func (handler *handler) listNodePackages(writer http.ResponseWriter, request *http.Request) {
	query, err := parseNodePackageQuery(request.URL.Query())
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	result, err := handler.dependencies.NodePackages.Search(query)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *handler) getNodePackage(writer http.ResponseWriter, request *http.Request) {
	names := request.URL.Query()["name"]
	if len(names) != 1 || names[0] == "" {
		writeRequestError(writer, request, errInvalidNodePackageQuery)
		return
	}
	detail, err := handler.dependencies.NodePackages.Get(names[0])
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func parseNodePackageQuery(values url.Values) (nodeindex.Query, error) {
	query := nodeindex.Query{Categories: append([]string{}, values["category"]...), CompatibleOnly: true, Limit: 50}
	if !hasAtMostOne(values, "q") || !hasAtMostOne(values, "compatible") || !hasAtMostOne(values, "limit") || !hasAtMostOne(values, "offset") {
		return nodeindex.Query{}, errInvalidNodePackageQuery
	}
	if len(query.Categories) > nodeindex.MaxCategories {
		return nodeindex.Query{}, errInvalidNodePackageQuery
	}
	for _, category := range query.Categories {
		if !utf8.ValidString(category) || utf8.RuneCountInString(category) > nodeindex.MaxCategoryLength || !nodePackageCategoryPattern.MatchString(category) {
			return nodeindex.Query{}, errInvalidNodePackageQuery
		}
	}
	if text := values.Get("q"); !utf8.ValidString(text) || utf8.RuneCountInString(text) > nodeindex.MaxQueryLength {
		return nodeindex.Query{}, errInvalidNodePackageQuery
	} else {
		query.Text = text
	}
	if values.Has("compatible") {
		switch values.Get("compatible") {
		case "true":
			query.CompatibleOnly = true
		case "false":
			query.CompatibleOnly = false
		default:
			return nodeindex.Query{}, errInvalidNodePackageQuery
		}
	}
	if values.Has("limit") {
		limit, err := strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > nodeindex.MaxSearchLimit {
			return nodeindex.Query{}, errInvalidNodePackageQuery
		}
		query.Limit = limit
	}
	if values.Has("offset") {
		offset, err := strconv.Atoi(values.Get("offset"))
		if err != nil || offset < 0 || offset > nodeindex.MaxSearchOffset {
			return nodeindex.Query{}, errInvalidNodePackageQuery
		}
		query.Offset = offset
	}
	return query, nil
}

func hasAtMostOne(values url.Values, key string) bool {
	return len(values[key]) <= 1
}
