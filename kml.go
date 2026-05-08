package main

import (
	"fmt"
	"os"
	"strings"
)

type kmlField struct{ Name, Value string }

// writeKMLFile 写出标准 KML Polygon。fields 顺序保留;空 Value 跳过。
// 调用者负责:数值格式化、skip-if-exists、[saved]/[skip] 日志。
func writeKMLFile(kmlPath, displayName string, ring [][]float64, fields []kmlField) error {
	var coords strings.Builder
	for _, p := range ring {
		if len(p) >= 2 {
			fmt.Fprintf(&coords, "%f,%f,0 ", p[0], p[1])
		}
	}

	var ext strings.Builder
	ext.WriteString("      <ExtendedData>\n")
	for _, f := range fields {
		if f.Value == "" {
			continue
		}
		fmt.Fprintf(&ext, "        <Data name=\"%s\"><value>%s</value></Data>\n", f.Name, f.Value)
	}
	ext.WriteString("      </ExtendedData>")

	kml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2">
  <Document>
    <Style id="polyStyle">
      <LineStyle>
        <color>ff0000ff</color>
        <width>2</width>
      </LineStyle>
      <PolyStyle>
        <color>7f0000ff</color>
        <fill>1</fill>
        <outline>1</outline>
      </PolyStyle>
    </Style>
    <Placemark>
      <name>%s</name>
      <styleUrl>#polyStyle</styleUrl>
%s
      <Polygon>
        <outerBoundaryIs>
          <LinearRing>
            <coordinates>%s</coordinates>
          </LinearRing>
        </outerBoundaryIs>
      </Polygon>
    </Placemark>
  </Document>
</kml>`, displayName, ext.String(), strings.TrimSpace(coords.String()))

	if err := os.WriteFile(kmlPath, []byte(kml), 0644); err != nil {
		return fmt.Errorf("write kml: %w", err)
	}
	return nil
}
