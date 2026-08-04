package dataset

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ValidateLabels(root string) (ValidationReport, error) {
	root, err := NormalizeRoot(root)
	if err != nil {
		return ValidationReport{}, err
	}
	classes, err := LoadClasses(root)
	if err != nil {
		return ValidationReport{}, err
	}
	paths := Paths(root)
	images, err := ListImages(root)
	if err != nil {
		return ValidationReport{}, err
	}
	report := ValidationReport{
		ImageCount:    len(images),
		InvalidLabels: map[string][]string{},
		ClassCounts:   map[string]int{},
	}
	for _, image := range images {
		stem := strings.TrimSuffix(image.Name, filepath.Ext(image.Name))
		labelPath := filepath.Join(paths.LabelsDir, stem+".txt")
		info, err := os.Stat(labelPath)
		if errors.Is(err, os.ErrNotExist) {
			report.MissingLabels = append(report.MissingLabels, image.Name)
			continue
		}
		if err != nil {
			report.InvalidLabels[image.Name] = []string{err.Error()}
			continue
		}
		report.LabelCount++
		if info.Size() == 0 {
			report.EmptyLabels = append(report.EmptyLabels, image.Name)
			continue
		}
		boxes, errors := validateOneLabel(labelPath, classes)
		if len(errors) > 0 {
			report.InvalidLabels[image.Name] = errors
			continue
		}
		report.ValidLabels++
		report.TotalBoxes += len(boxes)
		for _, box := range boxes {
			key := strconv.Itoa(box.ClassID)
			if box.ClassID >= 0 && box.ClassID < len(classes) {
				key = classes[box.ClassID]
			}
			report.ClassCounts[key]++
		}
	}
	return report, nil
}

type yoloBox struct {
	ClassID int
}

func validateOneLabel(path string, classes []string) ([]yoloBox, []string) {
	file, err := os.Open(path)
	if err != nil {
		return nil, []string{err.Error()}
	}
	defer file.Close()

	var boxes []yoloBox
	var problems []string
	lineNo := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 5 {
			problems = append(problems, fmt.Sprintf("第 %d 行：YOLO标签应有 5 列，实际有 %d 列", lineNo, len(parts)))
			continue
		}
		classID, err := strconv.Atoi(parts[0])
		if err != nil {
			problems = append(problems, fmt.Sprintf("第 %d 行：类别编号不是整数", lineNo))
			continue
		}
		if classID < 0 || (len(classes) > 0 && classID >= len(classes)) {
			problems = append(problems, fmt.Sprintf("第 %d 行：类别编号 %d 超出 classes.txt 范围", lineNo, classID))
		}
		values := make([]float64, 4)
		for i := 1; i < 5; i++ {
			value, err := strconv.ParseFloat(parts[i], 64)
			if err != nil {
				problems = append(problems, fmt.Sprintf("第 %d 行：第 %d 个坐标不是数字", lineNo, i))
				continue
			}
			values[i-1] = value
		}
		x, y, w, h := values[0], values[1], values[2], values[3]
		if x < 0 || x > 1 || y < 0 || y > 1 {
			problems = append(problems, fmt.Sprintf("第 %d 行：中心点坐标必须在 0 到 1 之间", lineNo))
		}
		if w <= 0 || w > 1 || h <= 0 || h > 1 {
			problems = append(problems, fmt.Sprintf("第 %d 行：宽度和高度必须大于 0 且不超过 1", lineNo))
		}
		if x-w/2 < 0 || x+w/2 > 1 || y-h/2 < 0 || y+h/2 > 1 {
			problems = append(problems, fmt.Sprintf("第 %d 行：标注框超出图片边界", lineNo))
		}
		boxes = append(boxes, yoloBox{ClassID: classID})
	}
	if err := scanner.Err(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(boxes) == 0 && len(problems) == 0 {
		problems = append(problems, "标签文件里没有目标框")
	}
	return boxes, problems
}
