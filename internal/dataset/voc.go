package dataset

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type vocAnnotation struct {
	Filename string      `xml:"filename"`
	Size     vocSize     `xml:"size"`
	Objects  []vocObject `xml:"object"`
}

type vocSize struct {
	Width  float64 `xml:"width"`
	Height float64 `xml:"height"`
}

type vocObject struct {
	Name string  `xml:"name"`
	Box  vocBBox `xml:"bndbox"`
}

type vocBBox struct {
	XMin float64 `xml:"xmin"`
	YMin float64 `xml:"ymin"`
	XMax float64 `xml:"xmax"`
	YMax float64 `xml:"ymax"`
}

type labelMeAnnotation struct {
	ImagePath   string         `json:"imagePath"`
	ImageWidth  float64        `json:"imageWidth"`
	ImageHeight float64        `json:"imageHeight"`
	Shapes      []labelMeShape `json:"shapes"`
}

type labelMeShape struct {
	Label  string      `json:"label"`
	Points [][]float64 `json:"points"`
}

func ConvertAnnotationsToYOLO(root string) (ConvertReport, error) {
	vocReport, err := ConvertVOCToYOLO(root)
	if err != nil {
		return ConvertReport{}, err
	}
	labelMeReport, err := ConvertLabelMeToYOLO(root)
	if err != nil {
		return ConvertReport{}, err
	}
	return mergeConvertReports(vocReport, labelMeReport), nil
}

func ConvertVOCToYOLO(root string) (ConvertReport, error) {
	root, err := NormalizeRoot(root)
	if err != nil {
		return ConvertReport{}, err
	}
	paths := Paths(root)
	classes, err := LoadClasses(root)
	if err != nil {
		return ConvertReport{}, err
	}
	classIDs := make(map[string]int, len(classes))
	for i, class := range classes {
		classIDs[class] = i
	}
	if len(classIDs) == 0 {
		return ConvertReport{}, fmt.Errorf("classes.txt 为空，请先添加类别")
	}
	if err := os.MkdirAll(paths.LabelsDir, 0o755); err != nil {
		return ConvertReport{}, err
	}
	entries, err := os.ReadDir(paths.XMLDir)
	if errors.Is(err, os.ErrNotExist) {
		return ConvertReport{}, nil
	}
	if err != nil {
		return ConvertReport{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	report := ConvertReport{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".xml") {
			continue
		}
		report.XMLFiles++
		xmlPath := filepath.Join(paths.XMLDir, entry.Name())
		converted, boxes, skipped, err := convertOneXML(xmlPath, paths.LabelsDir, classIDs)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}
		if converted {
			report.ConvertedFiles++
		}
		report.Boxes += boxes
		report.SkippedObjects += skipped
	}
	return report, nil
}

func ConvertLabelMeToYOLO(root string) (ConvertReport, error) {
	root, err := NormalizeRoot(root)
	if err != nil {
		return ConvertReport{}, err
	}
	paths := Paths(root)
	classes, err := LoadClasses(root)
	if err != nil {
		return ConvertReport{}, err
	}
	classIDs := make(map[string]int, len(classes))
	for i, class := range classes {
		classIDs[class] = i
	}
	if len(classIDs) == 0 {
		return ConvertReport{}, fmt.Errorf("classes.txt 为空，请先添加类别")
	}
	if err := os.MkdirAll(paths.LabelsDir, 0o755); err != nil {
		return ConvertReport{}, err
	}
	entries, err := os.ReadDir(paths.LabelMeDir)
	if errors.Is(err, os.ErrNotExist) {
		return ConvertReport{}, nil
	}
	if err != nil {
		return ConvertReport{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	report := ConvertReport{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		report.JSONFiles++
		jsonPath := filepath.Join(paths.LabelMeDir, entry.Name())
		converted, boxes, skipped, err := convertOneLabelMeJSON(jsonPath, paths.LabelsDir, classIDs)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}
		if converted {
			report.ConvertedFiles++
		}
		report.Boxes += boxes
		report.SkippedObjects += skipped
	}
	return report, nil
}

func convertOneXML(xmlPath, labelDir string, classIDs map[string]int) (bool, int, int, error) {
	data, err := os.ReadFile(xmlPath)
	if err != nil {
		return false, 0, 0, err
	}
	var ann vocAnnotation
	if err := xml.Unmarshal(data, &ann); err != nil {
		return false, 0, 0, err
	}
	if ann.Size.Width <= 0 || ann.Size.Height <= 0 {
		return false, 0, len(ann.Objects), fmt.Errorf("图片尺寸无效")
	}
	stem := strings.TrimSuffix(filepath.Base(ann.Filename), filepath.Ext(ann.Filename))
	if stem == "" {
		stem = strings.TrimSuffix(filepath.Base(xmlPath), filepath.Ext(xmlPath))
	}
	var lines []string
	skipped := 0
	for _, obj := range ann.Objects {
		classID, ok := classIDs[strings.TrimSpace(obj.Name)]
		if !ok {
			skipped++
			continue
		}
		xmin, ymin, xmax, ymax := obj.Box.XMin, obj.Box.YMin, obj.Box.XMax, obj.Box.YMax
		if xmax <= xmin || ymax <= ymin || xmin < 0 || ymin < 0 {
			skipped++
			continue
		}
		xCenter := ((xmin + xmax) / 2) / ann.Size.Width
		yCenter := ((ymin + ymax) / 2) / ann.Size.Height
		width := (xmax - xmin) / ann.Size.Width
		height := (ymax - ymin) / ann.Size.Height
		if !validNormalizedBox(xCenter, yCenter, width, height) {
			skipped++
			continue
		}
		lines = append(lines, fmt.Sprintf("%d %.6f %.6f %.6f %.6f", classID, xCenter, yCenter, width, height))
	}
	if len(lines) == 0 {
		return false, 0, skipped, nil
	}
	labelPath := filepath.Join(labelDir, stem+".txt")
	return true, len(lines), skipped, os.WriteFile(labelPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func convertOneLabelMeJSON(jsonPath, labelDir string, classIDs map[string]int) (bool, int, int, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return false, 0, 0, err
	}
	var ann labelMeAnnotation
	if err := json.Unmarshal(data, &ann); err != nil {
		return false, 0, 0, err
	}
	if ann.ImageWidth <= 0 || ann.ImageHeight <= 0 {
		return false, 0, len(ann.Shapes), fmt.Errorf("图片尺寸无效")
	}
	stem := strings.TrimSuffix(filepath.Base(ann.ImagePath), filepath.Ext(ann.ImagePath))
	if stem == "" {
		stem = strings.TrimSuffix(filepath.Base(jsonPath), filepath.Ext(jsonPath))
	}
	var lines []string
	skipped := 0
	for _, shape := range ann.Shapes {
		classID, ok := classIDs[strings.TrimSpace(shape.Label)]
		if !ok {
			skipped++
			continue
		}
		xmin, ymin, xmax, ymax, ok := pointBounds(shape.Points)
		if !ok || xmax <= xmin || ymax <= ymin || xmin < 0 || ymin < 0 {
			skipped++
			continue
		}
		xCenter := ((xmin + xmax) / 2) / ann.ImageWidth
		yCenter := ((ymin + ymax) / 2) / ann.ImageHeight
		width := (xmax - xmin) / ann.ImageWidth
		height := (ymax - ymin) / ann.ImageHeight
		if !validNormalizedBox(xCenter, yCenter, width, height) {
			skipped++
			continue
		}
		lines = append(lines, fmt.Sprintf("%d %.6f %.6f %.6f %.6f", classID, xCenter, yCenter, width, height))
	}
	if len(lines) == 0 {
		return false, 0, skipped, nil
	}
	labelPath := filepath.Join(labelDir, stem+".txt")
	return true, len(lines), skipped, os.WriteFile(labelPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func pointBounds(points [][]float64) (float64, float64, float64, float64, bool) {
	if len(points) == 0 {
		return 0, 0, 0, 0, false
	}
	xmin, ymin := math.Inf(1), math.Inf(1)
	xmax, ymax := math.Inf(-1), math.Inf(-1)
	for _, point := range points {
		if len(point) < 2 {
			return 0, 0, 0, 0, false
		}
		x, y := point[0], point[1]
		if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
			return 0, 0, 0, 0, false
		}
		xmin = math.Min(xmin, x)
		ymin = math.Min(ymin, y)
		xmax = math.Max(xmax, x)
		ymax = math.Max(ymax, y)
	}
	return xmin, ymin, xmax, ymax, true
}

func mergeConvertReports(reports ...ConvertReport) ConvertReport {
	var merged ConvertReport
	for _, report := range reports {
		merged.XMLFiles += report.XMLFiles
		merged.JSONFiles += report.JSONFiles
		merged.ConvertedFiles += report.ConvertedFiles
		merged.Boxes += report.Boxes
		merged.SkippedObjects += report.SkippedObjects
		merged.Errors = append(merged.Errors, report.Errors...)
	}
	return merged
}

func validNormalizedBox(xCenter, yCenter, width, height float64) bool {
	values := []float64{xCenter, yCenter, width, height}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return xCenter >= 0 && xCenter <= 1 &&
		yCenter >= 0 && yCenter <= 1 &&
		width > 0 && width <= 1 &&
		height > 0 && height <= 1
}
