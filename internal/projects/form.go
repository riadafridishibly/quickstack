package projects

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/riadafridishibly/quickstack/internal/db"
)

const (
	StatusActive   = "active"
	StatusArchived = "archived"

	maxNameLen        = 80
	maxDescriptionLen = 2000
	maxTitleLen       = 200
)

type projectForm struct {
	Name        string
	Description string
	Status      string
	Public      bool
	Errors      map[string]string
}

func projectFormFrom(r *http.Request) projectForm {
	return projectForm{
		Name:        strings.TrimSpace(r.PostFormValue("name")),
		Description: strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("description"), "\r\n", "\n")),
		Status:      r.PostFormValue("status"),
		Public:      r.PostFormValue("public") == "on",
		Errors:      map[string]string{},
	}
}

func projectFormOf(p db.Project) projectForm {
	return projectForm{Name: p.Name, Description: p.Description, Status: p.Status, Public: p.Public, Errors: map[string]string{}}
}

func (f *projectForm) validate() bool {
	switch {
	case f.Name == "":
		f.Errors["name"] = "Enter a name."
	case utf8.RuneCountInString(f.Name) > maxNameLen:
		f.Errors["name"] = "Use at most 80 characters."
	}
	if utf8.RuneCountInString(f.Description) > maxDescriptionLen {
		f.Errors["description"] = "Use at most 2,000 characters."
	}
	if f.Status != StatusActive && f.Status != StatusArchived {
		f.Errors["status"] = "Choose a status."
	}
	return len(f.Errors) == 0
}

// Task statuses, in board order.
const (
	Todo  = "todo"
	Doing = "doing"
	Done  = "done"
)

var Statuses = []string{Todo, Doing, Done}

func StatusLabel(s string) string {
	switch s {
	case Todo:
		return "To do"
	case Doing:
		return "In progress"
	case Done:
		return "Done"
	}
	return s
}

type taskForm struct {
	Title    string
	Assignee string
	Due      string
	Errors   map[string]string
	// Set by validate.
	assigneeID *int64
	dueOn      *time.Time
}

func taskFormFrom(r *http.Request) taskForm {
	return taskForm{
		Title:    strings.TrimSpace(r.PostFormValue("title")),
		Assignee: r.PostFormValue("assignee"),
		Due:      strings.TrimSpace(r.PostFormValue("due")),
		Errors:   map[string]string{},
	}
}

func taskFormOf(t db.Task) taskForm {
	f := taskForm{Title: t.Title, Errors: map[string]string{}}
	if t.AssigneeID != nil {
		f.Assignee = strconv.FormatInt(*t.AssigneeID, 10)
	}
	if t.DueOn != nil {
		f.Due = t.DueOn.Format(time.DateOnly)
	}
	return f
}

// validate checks the form against the workspace's members, who are the
// only people a task can be assigned to.
func (f *taskForm) validate(members []db.ListMembersRow) bool {
	switch {
	case f.Title == "":
		f.Errors["title"] = "Enter a title."
	case utf8.RuneCountInString(f.Title) > maxTitleLen:
		f.Errors["title"] = "Use at most 200 characters."
	}
	if f.Assignee != "" {
		id, err := strconv.ParseInt(f.Assignee, 10, 64)
		if err != nil || !slices.ContainsFunc(members, func(m db.ListMembersRow) bool { return m.ID == id }) {
			f.Errors["assignee"] = "Choose a member of the workspace."
		} else {
			f.assigneeID = &id
		}
	}
	if f.Due != "" {
		d, err := time.Parse(time.DateOnly, f.Due)
		if err != nil {
			f.Errors["due"] = "Enter a date such as 2026-12-31."
		} else {
			f.dueOn = &d
		}
	}
	return len(f.Errors) == 0
}

// parseOrder reads the board order an island sends: task ids, comma
// separated.
func parseOrder(s string) ([]int64, bool) {
	if s == "" {
		return nil, true
	}
	var ids []int64
	for part := range strings.SplitSeq(s, ",") {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 || len(ids) >= 1000 {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}
