package main

import (
	"encoding/json"
	"net"
	"net/http"
	"sync/atomic"
)

// 교사 전용 API. 학생도 같은 AP 에서 서버에 닿으므로 기록 열람·삭제는
// 반드시 교사 PC 본인(localhost)에서만 허용한다(classroom-quiz 의 localOnly 패턴).

// currentCourse 는 지금 진행 중인 코스 파일("날짜/파일") — 기록 탭에서 지우지 못하게 막는 데 쓴다.
var currentCourse atomic.Value

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			http.Error(w, "forbidden — 교사 화면은 교사 PC 에서만 열 수 있습니다.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func handleListCourses(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date != "" && !validName(date) {
		writeErr(w, http.StatusBadRequest, "잘못된 날짜")
		return
	}
	list, err := listCourseFiles(date)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cur, _ := currentCourse.Load().(string)
	writeJSON(w, http.StatusOK, map[string]any{"courses": list, "dates": listDates(), "current": cur})
}

func handleGetCourse(w http.ResponseWriter, r *http.Request) {
	cf, err := readCourseFile(r.PathValue("date"), r.PathValue("file"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"course": cf.Course, "runs": cf.Runs,
		"board": buildBoard(cf.Runs, cf.Tries), "date": cf.Date, "file": cf.File})
}

func handleDeleteCourse(w http.ResponseWriter, r *http.Request) {
	date, file := r.PathValue("date"), r.PathValue("file")
	if cur, _ := currentCourse.Load().(string); cur == date+"/"+file {
		writeErr(w, http.StatusBadRequest, "지금 진행 중인 코스는 지울 수 없어요. 새 코스를 만든 뒤 지우세요.")
		return
	}
	if err := deleteCourseFile(date, file); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/results.csv?kind=best|all&date=D[&file=F] — file 이 있으면 그 코스만, 없으면 그 날짜 전체(날짜도 없으면 전부).
func handleResultsCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date, file, kind := q.Get("date"), q.Get("file"), q.Get("kind")
	if date != "" && !validName(date) {
		writeErr(w, http.StatusBadRequest, "잘못된 날짜")
		return
	}
	var list []*CourseFile
	if file != "" {
		cf, err := readCourseFile(date, file)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		list = append(list, cf)
	} else {
		sums, err := listCourseFiles(date)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, s := range sums {
			if cf, err := readCourseFile(s.Date, s.File); err == nil {
				list = append(list, cf)
			}
		}
	}
	name := "잠수함-"
	if kind == "all" {
		name += "전체주행"
	} else {
		name += "최고기록"
	}
	switch {
	case len(list) == 1 && file != "":
		name += "-" + list[0].Date + "-" + list[0].Course.Name
	case date != "":
		name += "-" + date
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlPathEscape(name+".csv"))
	_, _ = w.Write(coursesCSV(list, kind))
}

func urlPathEscape(s string) string {
	const hexd = "0123456789ABCDEF"
	out := make([]byte, 0, len(s)*3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '%', hexd[c>>4], hexd[c&15])
		}
	}
	return string(out)
}
