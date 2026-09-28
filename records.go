package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 코스 하나 = 파일 하나: exe 옆 results/<날짜>/<시각>-<코스id>.json
// 코스 설정(시드 포함 — 장애물은 시드로 다시 만든다), 학생별 도전 횟수, 공식 주행 기록 전부가 들어 있다.
// 공식 주행이 끝날 때마다 통째로 다시 쓴다(임시 파일 → 이름 바꾸기). 서버를 다시 켜면 가장 최근 코스를 이어서 한다.

type CourseFile struct {
	Course Course         `json:"course"`
	Tries  map[string]int `json:"tries"` // "학번|이름" → 공식 출발 횟수
	Runs   []RunRecord    `json:"runs"`
	Date   string         `json:"-"`
	File   string         `json:"-"`
}

func resultsDir() string { return filepath.Join(exeDir(), "results") }

func saveCourseFile(cf *CourseFile) {
	dir := filepath.Join(resultsDir(), cf.Date)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("결과 폴더 만들기 실패: %v", err)
		return
	}
	b, _ := json.MarshalIndent(cf, "", "  ")
	p := filepath.Join(dir, cf.File)
	if err := os.WriteFile(p+".tmp", b, 0644); err != nil {
		log.Printf("결과 저장 실패: %v", err)
		return
	}
	// 교사 화면이 같은 파일을 읽는 순간과 겹치면 윈도우에서 이름 바꾸기가 잠깐 실패할 수 있어 몇 번 다시 시도한다
	var err error
	for try := 0; try < 5; try++ {
		if err = os.Rename(p+".tmp", p); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log.Printf("결과 저장 실패: %v", err)
}

func readCourseFile(date, file string) (*CourseFile, error) {
	if !validName(date) || !validName(file) || !strings.HasSuffix(file, ".json") {
		return nil, errors.New("잘못된 경로")
	}
	b, err := os.ReadFile(filepath.Join(resultsDir(), date, file))
	if err != nil {
		return nil, err
	}
	var cf CourseFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, err
	}
	if cf.Tries == nil {
		cf.Tries = map[string]int{}
	}
	if cf.Runs == nil {
		cf.Runs = []RunRecord{}
	}
	cf.Date, cf.File = date, file
	return &cf, nil
}

// loadLatestCourse 는 가장 최근 코스 파일(없으면 nil).
func loadLatestCourse() *CourseFile {
	list, err := listCourseFiles("")
	if err != nil || len(list) == 0 {
		return nil
	}
	cf, err := readCourseFile(list[0].Date, list[0].File)
	if err != nil {
		log.Printf("최근 코스 불러오기 실패: %v", err)
		return nil
	}
	return cf
}

// validName 은 경로 조작을 막는다(영숫자·-·_·. 만, .. 금지).
func validName(name string) bool {
	if name == "" || len(name) > 64 || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

type CourseSummary struct {
	Date      string `json:"date"`
	File      string `json:"file"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Length    int    `json:"length"`
	Diff      string `json:"diff"`
	CreatedAt string `json:"createdAt"`
	Players   int    `json:"players"`
	Runs      int    `json:"runs"`
	Top       int    `json:"top"`
	TopName   string `json:"topName"`
}

// listCourseFiles 는 저장된 코스를 최근 순으로. date 가 비어 있지 않으면 그 날짜만.
func listCourseFiles(date string) ([]CourseSummary, error) {
	root := resultsDir()
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []CourseSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []CourseSummary{}
	for _, d := range dirs {
		if !d.IsDir() || !validName(d.Name()) || (date != "" && d.Name() != date) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			cf, err := readCourseFile(d.Name(), f.Name())
			if err != nil {
				continue
			}
			board := buildBoard(cf.Runs, cf.Tries)
			s := CourseSummary{Date: cf.Date, File: cf.File, ID: cf.Course.ID, Name: cf.Course.Name,
				Length: cf.Course.Length, Diff: cf.Course.Diff, CreatedAt: cf.Course.CreatedAt,
				Players: len(board), Runs: len(cf.Runs)}
			if len(board) > 0 {
				s.Top, s.TopName = board[0].Score, board[0].Name
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].File > out[j].File
	})
	return out, nil
}

func listDates() []string {
	dirs, err := os.ReadDir(resultsDir())
	out := []string{}
	if err != nil {
		return out
	}
	for _, d := range dirs {
		if d.IsDir() && validName(d.Name()) {
			out = append(out, d.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func deleteCourseFile(date, file string) error {
	if !validName(date) || !validName(file) || !strings.HasSuffix(file, ".json") {
		return errors.New("잘못된 경로")
	}
	return os.Remove(filepath.Join(resultsDir(), date, file))
}

func yesNo(b bool) string {
	if b {
		return "완주"
	}
	return "침몰"
}

// coursesCSV 는 엑셀에서 바로 열리는 CSV. kind=best: 학생별 최고 기록(코스당 학생 한 줄, 순위 포함),
// kind=all: 모든 공식 주행 한 줄씩.
func coursesCSV(list []*CourseFile, kind string) []byte {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // 엑셀이 UTF-8 로 인식하도록 BOM
	w := csv.NewWriter(&buf)
	if kind == "all" {
		_ = w.Write([]string{"날짜", "코스", "길이(m)", "난이도", "학번", "이름", "도전 번호", "점수", "거리(m)", "진주",
			"남은 선체", "결과", "충돌 횟수", "주행 시간(초)", "시각"})
		for _, cf := range list {
			for _, r := range cf.Runs {
				t, _ := time.Parse(time.RFC3339, r.At)
				_ = w.Write([]string{cf.Date, cf.Course.Name, strconv.Itoa(cf.Course.Length), diffKo[cf.Course.Diff],
					r.SID, r.Name, strconv.Itoa(r.Try), strconv.Itoa(r.Score), strconv.Itoa(r.Dist), strconv.Itoa(r.Pearls),
					strconv.Itoa(r.HP), yesNo(r.Finished), strconv.Itoa(r.Hits), strconv.Itoa(r.Ticks / 60), t.Format("15:04:05")})
			}
		}
	} else {
		_ = w.Write([]string{"날짜", "코스", "길이(m)", "난이도", "순위", "학번", "이름", "최고 점수", "거리(m)", "진주",
			"남은 선체", "결과", "도전 횟수"})
		for _, cf := range list {
			for _, b := range buildBoard(cf.Runs, cf.Tries) {
				_ = w.Write([]string{cf.Date, cf.Course.Name, strconv.Itoa(cf.Course.Length), diffKo[cf.Course.Diff],
					strconv.Itoa(b.Rank), b.SID, b.Name, strconv.Itoa(b.Score), strconv.Itoa(b.Dist), strconv.Itoa(b.Pearls),
					strconv.Itoa(b.HP), yesNo(b.Finished), strconv.Itoa(b.Tries)})
			}
		}
	}
	w.Flush()
	return buf.Bytes()
}
