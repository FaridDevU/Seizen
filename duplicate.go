package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type ProjectCandidate struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	GitRemote *string `json:"gitRemote"`
}

type DuplicateGroup struct {
	Key      string             `json:"key"`
	Title    string             `json:"title"`
	Variants []DuplicateVariant `json:"variants"`
}

type DuplicateVariant struct {
	ProjectID string `json:"projectId"`
	Label     string `json:"label"`
}

func (a *App) DetectDuplicateGroups(projects []ProjectCandidate) []DuplicateGroup {
	return detectDuplicateGroups(projects)
}

func (a *App) GroupDuplicate(group DuplicateGroup) error {
	if len(group.Variants) == 0 {
		return nil
	}
	if strings.TrimSpace(group.Title) == "" {
		return errors.New("the group needs a title")
	}
	seen := make(map[string]struct{}, len(group.Variants))
	for _, variant := range group.Variants {
		if strings.TrimSpace(variant.ProjectID) == "" || strings.TrimSpace(variant.Label) == "" {
			return errors.New("each variant needs a project and a label")
		}
		if _, exists := seen[variant.ProjectID]; exists {
			return errors.New("a project cannot repeat in the same group")
		}
		seen[variant.ProjectID] = struct{}{}
	}

	groupID, err := newUUID()
	if err != nil {
		return fmt.Errorf("could not generate the group identifier: %w", err)
	}
	arguments := []any{groupID, group.Title}
	cases := make([]string, 0, len(group.Variants))
	ids := make([]string, 0, len(group.Variants))
	for _, variant := range group.Variants {
		idPosition := len(arguments) + 1
		arguments = append(arguments, variant.ProjectID, variant.Label)
		cases = append(cases, fmt.Sprintf("WHEN ?%d THEN ?%d", idPosition, idPosition+1))
		ids = append(ids, fmt.Sprintf("?%d", idPosition))
	}

	ctx := a.context()
	pool, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not start the grouping: %w", err)
	}
	defer tx.Rollback()
	query := `UPDATE projects
SET group_id = ?1, group_title = ?2, updated_at = ` + projectNow + `,
    variant_label = CASE id ` + strings.Join(cases, " ") + ` ELSE variant_label END
WHERE id IN (` + strings.Join(ids, ", ") + `)`
	result, err := tx.ExecContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("could not group the versions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("could not verify the grouping: %w", err)
	}
	if affected != int64(len(group.Variants)) {
		return errors.New("not all projects to be grouped were found")
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("could not complete the grouping: %w", err)
	}
	return nil
}

// UngroupDuplicate undoes a version grouping: it clears the group, title
// and variant label on every project in the group.
func (a *App) UngroupDuplicate(groupID string) error {
	if strings.TrimSpace(groupID) == "" {
		return errors.New("the group identifier is not valid")
	}
	ctx := a.context()
	pool, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	result, err := pool.ExecContext(ctx, `UPDATE projects
SET group_id = NULL, group_title = NULL, variant_label = NULL, updated_at = `+projectNow+`
WHERE group_id = ?`, groupID)
	if err != nil {
		return fmt.Errorf("could not ungroup: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("could not verify the ungrouping: %w", err)
	}
	if affected == 0 {
		return errors.New("the group was not found")
	}
	return nil
}

func detectDuplicateGroups(projects []ProjectCandidate) []DuplicateGroup {
	if len(projects) < 2 {
		return []DuplicateGroup{}
	}
	parents := make([]int, len(projects))
	for index := range parents {
		parents[index] = index
	}
	names := make(map[string]int)
	remotes := make(map[string]int)
	for index, project := range projects {
		if key := normalizeProjectName(project.Name); key != "" {
			if previous, exists := names[key]; exists {
				union(parents, previous, index)
			}
			names[key] = index
		}
		if project.GitRemote != nil {
			if key, ok := normalizeRemote(*project.GitRemote); ok {
				if previous, exists := remotes[key]; exists {
					union(parents, previous, index)
				}
				remotes[key] = index
			}
		}
	}

	grouped := make(map[int][]int)
	for index := range projects {
		root := find(parents, index)
		grouped[root] = append(grouped[root], index)
	}
	indices := make([][]int, 0, len(grouped))
	for _, group := range grouped {
		if len(group) >= 2 {
			indices = append(indices, group)
		}
	}
	sort.Slice(indices, func(left, right int) bool {
		return indices[left][0] < indices[right][0]
	})
	groups := make([]DuplicateGroup, 0, len(indices))
	for _, group := range indices {
		groups = append(groups, buildDuplicateGroup(projects, group))
	}
	return groups
}

func normalizeProjectName(name string) string {
	tokens := nameTokens(name)
	original := append([]string(nil), tokens...)
	for len(tokens) > 0 && isVariantSuffix(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 {
		tokens = original
	}
	return strings.Join(tokens, "-")
}

func nameTokens(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsNumber(character)
	})
}

func isVariantSuffix(token string) bool {
	switch token {
	case "copia", "copy", "final", "ahora", "si", "sí", "backup", "old":
		return true
	}
	if len(token) < 2 || token[0] != 'v' {
		return false
	}
	for _, character := range token[1:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func normalizeRemote(value string) (string, bool) {
	remote := strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
	if remote == "" {
		return "", false
	}
	if path, ok := strings.CutPrefix(remote, "git@github.com:"); ok {
		remote = "github.com/" + path
	} else if path, ok := strings.CutPrefix(remote, "https://github.com/"); ok {
		remote = "github.com/" + path
	}
	remote = strings.TrimSuffix(remote, ".git")
	return remote, true
}

func find(parents []int, index int) int {
	if parents[index] != index {
		parents[index] = find(parents, parents[index])
	}
	return parents[index]
}

func union(parents []int, left, right int) {
	left = find(parents, left)
	right = find(parents, right)
	if left != right {
		parents[right] = left
	}
}

func buildDuplicateGroup(projects []ProjectCandidate, indices []int) DuplicateGroup {
	first := projects[indices[0]]
	firstNameKey := normalizeProjectName(first.Name)
	sameName := true
	for _, index := range indices {
		if normalizeProjectName(projects[index].Name) != firstNameKey {
			sameName = false
			break
		}
	}

	sharedRemote, hasSharedRemote := "", false
	if first.GitRemote != nil {
		sharedRemote, hasSharedRemote = normalizeRemote(*first.GitRemote)
	}
	if hasSharedRemote {
		for _, index := range indices {
			projectRemote := projects[index].GitRemote
			remote, ok := "", false
			if projectRemote != nil {
				remote, ok = normalizeRemote(*projectRemote)
			}
			if !ok || remote != sharedRemote {
				hasSharedRemote = false
				break
			}
		}
	}

	titleKey := firstNameKey
	if !sameName && hasSharedRemote {
		repository := sharedRemote
		if slash := strings.LastIndexByte(sharedRemote, '/'); slash >= 0 {
			repository = sharedRemote[slash+1:]
		}
		if repository != "" {
			titleKey = repository
		}
	}
	key := firstNameKey
	if !sameName && hasSharedRemote {
		key = "remote:" + sharedRemote
	}

	usedLabels := make(map[string]struct{}, len(indices))
	nextVariant := 1
	variants := make([]DuplicateVariant, 0, len(indices))
	for _, index := range indices {
		project := projects[index]
		label, preferred := preferredLabel(project.Name, firstNameKey)
		if preferred {
			if _, used := usedLabels[label]; used {
				preferred = false
			}
		}
		if !preferred {
			for {
				label = fmt.Sprintf("variant %d", nextVariant)
				nextVariant++
				if _, used := usedLabels[label]; !used {
					break
				}
			}
		}
		usedLabels[label] = struct{}{}
		variants = append(variants, DuplicateVariant{ProjectID: project.ID, Label: label})
	}
	return DuplicateGroup{Key: key, Title: titleCase(titleKey), Variants: variants}
}

func preferredLabel(name, base string) (string, bool) {
	tokens := nameTokens(name)
	if strings.Join(tokens, "-") == base {
		return "main", true
	}
	if hasToken(tokens, "claude") {
		return "Claude changes", true
	}
	if hasToken(tokens, "design", "diseño", "diseno", "experiment", "experimento") {
		return "design experiment", true
	}
	if hasToken(tokens, "final") {
		return "stable version", true
	}
	if hasToken(tokens, "copia", "copy", "backup", "old", "anterior") {
		return "previous version", true
	}
	return "", false
}

func hasToken(tokens []string, wanted ...string) bool {
	for _, token := range tokens {
		for _, value := range wanted {
			if token == value {
				return true
			}
		}
	}
	return false
}

func titleCase(key string) string {
	words := make([]string, 0)
	for _, word := range strings.Split(key, "-") {
		characters := []rune(word)
		if len(characters) > 0 {
			characters[0] = unicode.ToUpper(characters[0])
			words = append(words, string(characters))
		}
	}
	return strings.Join(words, " ")
}
