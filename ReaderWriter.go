/*
Copyright (C) 2025 [fmecool]

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/
package Gogeo

/*
#include "osgeo_utils.h"
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

// PostGISConfig PostGIS连接配置
type PostGISConfig struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
	Schema   string
	Table    string
}

// GDALLayer 包装GDAL图层
type GDALLayer struct {
	layer   C.OGRLayerH
	dataset C.OGRDataSourceH
	driver  C.OGRSFDriverH
}

// PostGISReader PostGIS读取器
type PostGISReader struct {
	config *PostGISConfig
}

// NewPostGISReader 创建新的PostGIS读取器
func NewPostGISReader(config *PostGISConfig) *PostGISReader {
	return &PostGISReader{
		config: config,
	}
}

// ReadGeometryTable 读取PostGIS几何表数据
func (r *PostGISReader) ReadGeometryTable() (*GDALLayer, error) {
	// 初始化GDAL
	InitializeGDAL()
	// 构建连接字符串
	connStr := fmt.Sprintf("PG:host=%s port=%s dbname=%s user=%s password=%s",
		r.config.Host, r.config.Port, r.config.Database,
		r.config.User, r.config.Password)

	cConnStr := C.CString(connStr)
	defer C.free(unsafe.Pointer(cConnStr))

	// 获取PostgreSQL驱动
	driver := C.OGRGetDriverByName(C.CString("PostgreSQL"))
	if driver == nil {
		return nil, fmt.Errorf("无法获取PostgreSQL驱动")
	}

	// 打开数据源
	dataset := C.OGROpen(cConnStr, C.int(0), nil) // 0表示只读
	if dataset == nil {
		return nil, fmt.Errorf("无法连接到PostGIS数据库: %s", connStr)
	}

	// 构建图层名称（包含schema）
	var layerName string
	if r.config.Schema != "" {
		layerName = fmt.Sprintf("%s.%s", r.config.Schema, r.config.Table)
	} else {
		layerName = r.config.Table
	}

	cLayerName := C.CString(layerName)
	defer C.free(unsafe.Pointer(cLayerName))

	// 获取图层
	layer := C.OGR_DS_GetLayerByName(dataset, cLayerName)
	if layer == nil {
		C.OGR_DS_Destroy(dataset)
		return nil, fmt.Errorf("无法找到图层: %s", layerName)
	}

	gdalLayer := &GDALLayer{
		layer:   layer,
		dataset: dataset,
		driver:  driver,
	}

	// 设置finalizer以确保资源清理
	runtime.SetFinalizer(gdalLayer, (*GDALLayer).cleanup)

	return gdalLayer, nil
}

// GetFeatureCount 获取要素数量
func (gl *GDALLayer) GetFeatureCount() int {
	return int(C.OGR_L_GetFeatureCount(gl.layer, C.int(1))) // 1表示强制计算
}

// GetLayerDefn 获取图层定义
func (gl *GDALLayer) GetLayerDefn() C.OGRFeatureDefnH {
	return C.OGR_L_GetLayerDefn(gl.layer)
}

// GetFieldCount 获取字段数量
func (gl *GDALLayer) GetFieldCount() int {
	defn := gl.GetLayerDefn()
	return int(C.OGR_FD_GetFieldCount(defn))
}

// GetFieldName 获取字段名称
func (gl *GDALLayer) GetFieldName(index int) string {
	defn := gl.GetLayerDefn()
	fieldDefn := C.OGR_FD_GetFieldDefn(defn, C.int(index))
	if fieldDefn == nil {
		return ""
	}
	return C.GoString(C.OGR_Fld_GetNameRef(fieldDefn))
}

// GetGeometryType 获取几何类型
func (gl *GDALLayer) GetGeometryType() string {
	defn := gl.GetLayerDefn()
	geomType := C.OGR_FD_GetGeomType(defn)
	return C.GoString(C.OGRGeometryTypeToName(geomType))
}

// GetLayerName 获取图层名称
func (gl *GDALLayer) GetLayerName() string {
	if gl.layer == nil {
		return ""
	}
	layerName := C.OGR_L_GetName(gl.layer)
	if layerName == nil {
		return ""
	}
	return C.GoString(layerName)
}

// GetSpatialRef 获取空间参考系统
func (gl *GDALLayer) GetSpatialRef() C.OGRSpatialReferenceH {
	return C.OGR_L_GetSpatialRef(gl.layer)
}

// ResetReading 重置读取位置
func (gl *GDALLayer) ResetReading() {
	C.OGR_L_ResetReading(gl.layer)
}

// GetNextFeature 获取下一个要素
func (gl *GDALLayer) GetNextFeature() C.OGRFeatureH {
	return C.OGR_L_GetNextFeature(gl.layer)
}

// PrintLayerInfo 打印图层信息
func (gl *GDALLayer) PrintLayerInfo() {
	fmt.Printf("图层信息:\n")
	fmt.Printf("  要素数量: %d\n", gl.GetFeatureCount())
	fmt.Printf("  几何类型: %s\n", gl.GetGeometryType())
	fmt.Printf("  字段数量: %d\n", gl.GetFieldCount())

	fmt.Printf("  字段列表:\n")
	for i := 0; i < gl.GetFieldCount(); i++ {
		fmt.Printf("    %d: %s\n", i, gl.GetFieldName(i))
	}

	// 打印空间参考系统信息
	srs := gl.GetSpatialRef()
	if srs != nil {
		var projStr *C.char
		C.OSRExportToProj4(srs, &projStr)
		if projStr != nil {
			fmt.Printf("  投影: %s\n", C.GoString(projStr))

		}
	}
}

// IterateFeatures 遍历所有要素
func (gl *GDALLayer) IterateFeatures(callback func(feature C.OGRFeatureH)) {
	gl.ResetReading()

	for {
		feature := gl.GetNextFeature()
		if feature == nil {
			break
		}

		callback(feature)

		// 释放要素
		C.OGR_F_Destroy(feature)
	}
}

// cleanup 清理资源
func (gl *GDALLayer) cleanup() {
	if gl.dataset != nil {
		C.OGR_DS_Destroy(gl.dataset)
		gl.dataset = nil
	}
}

// Close 手动关闭资源
func (gl *GDALLayer) Close() {
	gl.cleanup()
	runtime.SetFinalizer(gl, nil)
}

func MakePGReader(table string) *PostGISReader {
	con := MainConfig
	config := &PostGISConfig{
		Host:     con.Host,
		Port:     con.Port,
		Database: con.Dbname,
		User:     con.Username,
		Password: con.Password,
		Schema:   "public", // 可选，默认为public
		Table:    table,
	}
	// 创建读取器
	reader := NewPostGISReader(config)
	return reader
}

// FileGeoReader 文件地理数据读取器
type FileGeoReader struct {
	FilePath string
	FileType string // "shp", "gdb"
}

// NewFileGeoReader 创建新的文件地理数据读取器
func NewFileGeoReader(filePath string) (*FileGeoReader, error) {
	// 检查文件是否存在
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("文件不存在: %s", filePath)
	}

	// 确定文件类型
	fileType, err := determineFileType(filePath)
	if err != nil {
		return nil, err
	}

	return &FileGeoReader{
		FilePath: filePath,
		FileType: fileType,
	}, nil
}

// determineFileType 确定文件类型
func determineFileType(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".shp":
		return "shp", nil
	case ".gdb":
		return "gdb", nil
	default:
		// 检查是否为文件夹（可能是GDB）
		if info, err := os.Stat(filePath); err == nil && info.IsDir() {
			// 检查是否为GDB文件夹
			if strings.HasSuffix(strings.ToLower(filePath), ".gdb") {
				return "gdb", nil
			}
		}
		return "", fmt.Errorf("不支持的文件类型: %s", ext)
	}
}

// ReadShapeFile 读取Shapefile
func (r *FileGeoReader) ReadShapeFile(layerName ...string) (*GDALLayer, error) {
	if r.FileType != "shp" {
		return nil, fmt.Errorf("文件类型不是Shapefile: %s", r.FileType)
	}

	// 初始化GDAL
	InitializeGDAL()

	cFilePath := C.CString(r.FilePath)
	defer C.free(unsafe.Pointer(cFilePath))

	// 获取Shapefile驱动
	driver := C.OGRGetDriverByName(C.CString("ESRI Shapefile"))
	if driver == nil {
		return nil, fmt.Errorf("无法获取Shapefile驱动")
	}

	// 打开数据源
	dataset := C.OGROpen(cFilePath, C.int(0), nil) // 0表示只读
	if dataset == nil {
		return nil, fmt.Errorf("无法打开Shapefile: %s", r.FilePath)
	}

	var layer C.OGRLayerH

	// 如果指定了图层名称，则按名称获取
	if len(layerName) > 0 && layerName[0] != "" {
		cLayerName := C.CString(layerName[0])
		defer C.free(unsafe.Pointer(cLayerName))
		layer = C.OGR_DS_GetLayerByName(dataset, cLayerName)
	} else {
		// 否则获取第一个图层
		if C.OGR_DS_GetLayerCount(dataset) > 0 {
			layer = C.OGR_DS_GetLayer(dataset, C.int(0))
		}
	}

	if layer == nil {
		C.OGR_DS_Destroy(dataset)
		return nil, fmt.Errorf("无法获取图层")
	}

	gdalLayer := &GDALLayer{
		layer:   layer,
		dataset: dataset,
		driver:  driver,
	}

	// 设置finalizer以确保资源清理
	runtime.SetFinalizer(gdalLayer, (*GDALLayer).cleanup)

	return gdalLayer, nil
}

// ReadGDBFile 读取GDB文件
func (r *FileGeoReader) ReadGDBFile(layerName ...string) (*GDALLayer, error) {
	if r.FileType != "gdb" {
		return nil, fmt.Errorf("文件类型不是GDB: %s", r.FileType)
	}

	// 初始化GDAL
	InitializeGDAL()

	cFilePath := C.CString(r.FilePath)
	defer C.free(unsafe.Pointer(cFilePath))

	// 获取FileGDB驱动
	driver := C.OGRGetDriverByName(C.CString("FileGDB"))
	if driver == nil {
		// 如果FileGDB驱动不可用，尝试OpenFileGDB驱动
		driver = C.OGRGetDriverByName(C.CString("OpenFileGDB"))
		if driver == nil {
			return nil, fmt.Errorf("无法获取GDB驱动（需要FileGDB或OpenFileGDB驱动）")
		}
	}

	// 打开数据源
	dataset := C.OGROpen(cFilePath, C.int(0), nil) // 0表示只读
	if dataset == nil {
		return nil, fmt.Errorf("无法打开GDB文件: %s", r.FilePath)
	}

	var layer C.OGRLayerH

	// 如果指定了图层名称，则按名称获取
	if len(layerName) > 0 && layerName[0] != "" {
		cLayerName := C.CString(layerName[0])
		defer C.free(unsafe.Pointer(cLayerName))
		layer = C.OGR_DS_GetLayerByName(dataset, cLayerName)
	} else {
		// 否则获取第一个图层
		if C.OGR_DS_GetLayerCount(dataset) > 0 {
			layer = C.OGR_DS_GetLayer(dataset, C.int(0))
		}
	}

	if layer == nil {
		C.OGR_DS_Destroy(dataset)
		return nil, fmt.Errorf("无法获取图层")
	}

	gdalLayer := &GDALLayer{
		layer:   layer,
		dataset: dataset,
		driver:  driver,
	}

	// 设置finalizer以确保资源清理
	runtime.SetFinalizer(gdalLayer, (*GDALLayer).cleanup)

	return gdalLayer, nil
}

// ReadLayer 通用读取图层方法
func (r *FileGeoReader) ReadLayer(layerName ...string) (*GDALLayer, error) {
	switch r.FileType {
	case "shp":
		return r.ReadShapeFile(layerName...)
	case "gdb":
		return r.ReadGDBFile(layerName...)
	default:
		return nil, fmt.Errorf("不支持的文件类型: %s", r.FileType)
	}
}

// ListLayers 列出所有图层
func (r *FileGeoReader) ListLayers() ([]string, error) {
	// 初始化GDAL
	InitializeGDAL()

	cFilePath := C.CString(r.FilePath)
	defer C.free(unsafe.Pointer(cFilePath))

	// 打开数据源
	dataset := C.OGROpen(cFilePath, C.int(0), nil)
	if dataset == nil {
		return nil, fmt.Errorf("无法打开文件: %s", r.FilePath)
	}
	defer C.OGR_DS_Destroy(dataset)

	layerCount := int(C.OGR_DS_GetLayerCount(dataset))
	layers := make([]string, 0, layerCount)

	for i := 0; i < layerCount; i++ {
		layer := C.OGR_DS_GetLayer(dataset, C.int(i))
		if layer != nil {
			layerName := C.GoString(C.OGR_L_GetName(layer))
			layers = append(layers, layerName)
		}
	}

	return layers, nil
}

// GetLayerInfo 获取图层信息
func (r *FileGeoReader) GetLayerInfo(layerName ...string) (map[string]interface{}, error) {
	layer, err := r.ReadLayer(layerName...)
	if err != nil {
		return nil, err
	}
	defer layer.Close()

	info := make(map[string]interface{})
	info["feature_count"] = layer.GetFeatureCount()
	info["geometry_type"] = layer.GetGeometryType()
	info["field_count"] = layer.GetFieldCount()

	// 获取字段信息
	fields := make([]map[string]interface{}, 0, layer.GetFieldCount())
	for i := 0; i < layer.GetFieldCount(); i++ {
		field := map[string]interface{}{
			"index": i,
			"name":  layer.GetFieldName(i),
			"type":  layer.GetFieldType(i),
		}
		fields = append(fields, field)
	}
	info["fields"] = fields

	// 获取空间参考系统信息
	srs := layer.GetSpatialRef()
	if srs != nil {
		var projStr *C.char
		C.OSRExportToProj4(srs, &projStr)
		if projStr != nil {
			info["projection"] = C.GoString(projStr)
			C.CPLFree(unsafe.Pointer(projStr))
		}
	}

	return info, nil
}

// GetFieldType 获取字段类型
func (gl *GDALLayer) GetFieldType(index int) string {
	defn := gl.GetLayerDefn()
	fieldDefn := C.OGR_FD_GetFieldDefn(defn, C.int(index))
	if fieldDefn == nil {
		return ""
	}
	fieldType := C.OGR_Fld_GetType(fieldDefn)
	return C.GoString(C.OGR_GetFieldTypeName(fieldType))
}

// 便捷函数

// MakeShapeFileReader 创建Shapefile读取器
func MakeShapeFileReader(filePath string) (*FileGeoReader, error) {
	return NewFileGeoReader(filePath)
}

// MakeGDBReader 创建GDB读取器
func MakeGDBReader(filePath string) (*FileGeoReader, error) {
	return NewFileGeoReader(filePath)
}

// ReadShapeFileLayer 直接读取Shapefile图层
func ReadShapeFileLayer(filePath string, layerName ...string) (*GDALLayer, error) {
	reader, err := NewFileGeoReader(filePath)
	if err != nil {
		return nil, err
	}
	return reader.ReadShapeFile(layerName...)
}

// ReadGDBLayer 直接读取GDB图层
func ReadGDBLayer(filePath string, layerName ...string) (*GDALLayer, error) {
	reader, err := NewFileGeoReader(filePath)
	if err != nil {
		return nil, err
	}
	return reader.ReadGDBFile(layerName...)
}

// ReadGeospatialFile 通用读取地理空间文件
func ReadGeospatialFile(filePath string, layerName ...string) (*GDALLayer, error) {
	reader, err := NewFileGeoReader(filePath)
	if err != nil {
		return nil, err
	}
	return reader.ReadLayer(layerName...)
}

// FileGeoWriter 文件地理数据写入器
type FileGeoWriter struct {
	FilePath  string
	FileType  string // "shp", "gdb"
	Overwrite bool   // 是否覆盖已存在的文件
}

// NewFileGeoWriter 创建新的文件地理数据写入器
func NewFileGeoWriter(filePath string, overwrite bool) (*FileGeoWriter, error) {
	// 确定文件类型
	fileType, err := determineFileTypeForWrite(filePath)
	if err != nil {
		return nil, err
	}

	return &FileGeoWriter{
		FilePath:  filePath,
		FileType:  fileType,
		Overwrite: overwrite,
	}, nil
}

// determineFileTypeForWrite 确定写入文件类型
func determineFileTypeForWrite(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".shp":
		return "shp", nil
	case ".gdb":
		return "gdb", nil
	default:
		// 检查是否以.gdb结尾的文件夹
		if strings.HasSuffix(strings.ToLower(filePath), ".gdb") {
			return "gdb", nil
		}
		return "", fmt.Errorf("不支持的文件类型: %s", ext)
	}
}

// WriteShapeFile 写入Shapefile
func (w *FileGeoWriter) WriteShapeFile(sourceLayer *GDALLayer, layerName string) error {
	if w.FileType != "shp" {
		return fmt.Errorf("文件类型不是Shapefile: %s", w.FileType)
	}

	// 初始化GDAL
	InitializeGDAL()

	// 如果需要覆盖，先删除已存在的文件
	if w.Overwrite {
		w.removeShapeFiles()
	}

	// 获取Shapefile驱动
	driver := C.OGRGetDriverByName(C.CString("ESRI Shapefile"))
	if driver == nil {
		return fmt.Errorf("无法获取Shapefile驱动")
	}

	// 创建数据源
	cFilePath := C.CString(w.FilePath)
	defer C.free(unsafe.Pointer(cFilePath))

	dataset := C.OGR_Dr_CreateDataSource(driver, cFilePath, nil)
	if dataset == nil {
		return fmt.Errorf("无法创建Shapefile: %s", w.FilePath)
	}
	defer C.OGR_DS_Destroy(dataset)

	// 获取源图层信息
	sourceDefn := sourceLayer.GetLayerDefn()
	geomType := C.OGR_FD_GetGeomType(sourceDefn)
	srs := sourceLayer.GetSpatialRef()

	// 创建图层
	cLayerName := C.CString(layerName)
	defer C.free(unsafe.Pointer(cLayerName))

	newLayer := C.OGR_DS_CreateLayer(dataset, cLayerName, srs, geomType, nil)
	if newLayer == nil {
		return fmt.Errorf("无法创建图层: %s", layerName)
	}

	// 复制字段定义
	err := w.copyFieldDefinitions(sourceDefn, newLayer)
	if err != nil {
		return err
	}

	// 复制要素
	err = w.copyFeatures(sourceLayer, newLayer)
	if err != nil {
		return err
	}

	return nil
}

// WriteGDBFile 写入GDB文件
func (w *FileGeoWriter) WriteGDBFile(sourceLayer *GDALLayer, layerName string) error {
	if w.FileType != "gdb" {
		return fmt.Errorf("文件类型不是GDB: %s", w.FileType)
	}

	// 初始化GDAL
	InitializeGDAL()

	// 获取FileGDB驱动
	driver := C.OGRGetDriverByName(C.CString("FileGDB"))
	if driver == nil {
		// 如果FileGDB驱动不可用，尝试OpenFileGDB驱动（但OpenFileGDB通常是只读的）
		return fmt.Errorf("无法获取FileGDB驱动（需要FileGDB驱动支持写入）")
	}

	cFilePath := C.CString(w.FilePath)
	defer C.free(unsafe.Pointer(cFilePath))

	var dataset C.OGRDataSourceH

	// 检查GDB是否已存在
	if _, err := os.Stat(w.FilePath); err == nil {
		if w.Overwrite {
			// 删除已存在的GDB
			os.RemoveAll(w.FilePath)
			// 创建新的GDB
			dataset = C.OGR_Dr_CreateDataSource(driver, cFilePath, nil)
		} else {
			// 打开已存在的GDB
			dataset = C.OGROpen(cFilePath, C.int(1), nil) // 1表示可写
		}
	} else {
		// 创建新的GDB
		dataset = C.OGR_Dr_CreateDataSource(driver, cFilePath, nil)
	}

	if dataset == nil {
		return fmt.Errorf("无法创建或打开GDB文件: %s", w.FilePath)
	}
	defer C.OGR_DS_Destroy(dataset)

	// 获取源图层信息
	sourceDefn := sourceLayer.GetLayerDefn()
	geomType := C.OGR_FD_GetGeomType(sourceDefn)
	srs := sourceLayer.GetSpatialRef()

	// 创建图层
	cLayerName := C.CString(layerName)
	defer C.free(unsafe.Pointer(cLayerName))

	newLayer := C.OGR_DS_CreateLayer(dataset, cLayerName, srs, geomType, nil)
	if newLayer == nil {
		return fmt.Errorf("无法创建图层: %s", layerName)
	}

	// 复制字段定义
	err := w.copyFieldDefinitions(sourceDefn, newLayer)
	if err != nil {
		return err
	}

	// 复制要素
	err = w.copyFeatures(sourceLayer, newLayer)
	if err != nil {
		return err
	}

	return nil
}

// WriteLayer 通用写入图层方法
func (w *FileGeoWriter) WriteLayer(sourceLayer *GDALLayer, layerName string) error {
	switch w.FileType {
	case "shp":
		return w.WriteShapeFile(sourceLayer, layerName)
	case "gdb":
		return w.WriteGDBFile(sourceLayer, layerName)
	default:
		return fmt.Errorf("不支持的文件类型: %s", w.FileType)
	}
}

// copyFieldDefinitions 复制字段定义
func (w *FileGeoWriter) copyFieldDefinitions(sourceDefn C.OGRFeatureDefnH, targetLayer C.OGRLayerH) error {
	fieldCount := int(C.OGR_FD_GetFieldCount(sourceDefn))

	for i := 0; i < fieldCount; i++ {
		sourceFieldDefn := C.OGR_FD_GetFieldDefn(sourceDefn, C.int(i))
		if sourceFieldDefn == nil {
			continue
		}

		// 创建新的字段定义
		fieldName := C.OGR_Fld_GetNameRef(sourceFieldDefn)
		fieldType := C.OGR_Fld_GetType(sourceFieldDefn)

		newFieldDefn := C.OGR_Fld_Create(fieldName, fieldType)
		if newFieldDefn == nil {
			return fmt.Errorf("无法创建字段定义")
		}

		// 复制字段属性
		C.OGR_Fld_SetWidth(newFieldDefn, C.OGR_Fld_GetWidth(sourceFieldDefn))
		C.OGR_Fld_SetPrecision(newFieldDefn, C.OGR_Fld_GetPrecision(sourceFieldDefn))

		// 添加字段到目标图层
		if C.OGR_L_CreateField(targetLayer, newFieldDefn, C.int(1)) != C.OGRERR_NONE {
			C.OGR_Fld_Destroy(newFieldDefn)
			return fmt.Errorf("无法创建字段: %s", C.GoString(fieldName))
		}

		C.OGR_Fld_Destroy(newFieldDefn)
	}

	return nil
}

// copyFeatures 复制要素
func (w *FileGeoWriter) copyFeatures(sourceLayer *GDALLayer, targetLayer C.OGRLayerH) error {
	sourceLayer.ResetReading()

	targetDefn := C.OGR_L_GetLayerDefn(targetLayer)

	for {
		sourceFeature := sourceLayer.GetNextFeature()
		if sourceFeature == nil {
			break
		}

		// 创建新要素
		newFeature := C.OGR_F_Create(targetDefn)
		if newFeature == nil {
			C.OGR_F_Destroy(sourceFeature)
			return fmt.Errorf("无法创建新要素")
		}

		// 复制几何
		geometry := C.OGR_F_GetGeometryRef(sourceFeature)
		if geometry != nil {
			// 克隆几何
			clonedGeom := C.OGR_G_Clone(geometry)
			if clonedGeom != nil {
				C.OGR_F_SetGeometry(newFeature, clonedGeom)
				C.OGR_G_DestroyGeometry(clonedGeom)
			}
		}

		// 复制字段值
		fieldCount := int(C.OGR_F_GetFieldCount(sourceFeature))
		for i := 0; i < fieldCount; i++ {
			if C.OGR_F_IsFieldSet(sourceFeature, C.int(i)) != 0 {
				// 根据字段类型复制值
				fieldDefn := C.OGR_F_GetFieldDefnRef(sourceFeature, C.int(i))
				fieldType := C.OGR_Fld_GetType(fieldDefn)

				switch fieldType {
				case C.OFTInteger:
					value := C.OGR_F_GetFieldAsInteger(sourceFeature, C.int(i))
					C.OGR_F_SetFieldInteger(newFeature, C.int(i), value)
				case C.OFTReal:
					value := C.OGR_F_GetFieldAsDouble(sourceFeature, C.int(i))
					C.OGR_F_SetFieldDouble(newFeature, C.int(i), value)
				case C.OFTString:
					value := C.OGR_F_GetFieldAsString(sourceFeature, C.int(i))
					C.OGR_F_SetFieldString(newFeature, C.int(i), value)
					// 可以添加更多字段类型的处理
				}
			}
		}

		// 添加要素到目标图层
		if C.OGR_L_CreateFeature(targetLayer, newFeature) != C.OGRERR_NONE {
			C.OGR_F_Destroy(newFeature)
			C.OGR_F_Destroy(sourceFeature)
			return fmt.Errorf("无法添加要素到目标图层")
		}

		C.OGR_F_Destroy(newFeature)
		C.OGR_F_Destroy(sourceFeature)
	}

	return nil
}

// removeShapeFiles 删除Shapefile相关文件
func (w *FileGeoWriter) removeShapeFiles() {
	baseName := strings.TrimSuffix(w.FilePath, filepath.Ext(w.FilePath))
	extensions := []string{".shp", ".shx", ".dbf", ".prj", ".cpg", ".qix", ".sbn", ".sbx"}

	for _, ext := range extensions {
		filePath := baseName + ext
		if _, err := os.Stat(filePath); err == nil {
			os.Remove(filePath)
		}
	}
}

// 便捷函数

// WriteShapeFileLayer 直接写入Shapefile图层
func WriteShapeFileLayer(sourceLayer *GDALLayer, filePath string, layerName string, overwrite bool) error {
	writer, err := NewFileGeoWriter(filePath, overwrite)
	if err != nil {
		return err
	}
	return writer.WriteShapeFile(sourceLayer, layerName)
}

// WriteGDBLayer 直接写入GDB图层
func WriteGDBLayer(sourceLayer *GDALLayer, filePath string, layerName string, overwrite bool) error {
	writer, err := NewFileGeoWriter(filePath, overwrite)
	if err != nil {
		return err
	}
	return writer.WriteGDBFile(sourceLayer, layerName)
}

// WriteGeospatialFile 通用写入地理空间文件
func WriteGeospatialFile(sourceLayer *GDALLayer, filePath string, layerName string, overwrite bool) error {
	writer, err := NewFileGeoWriter(filePath, overwrite)
	if err != nil {
		return err
	}
	return writer.WriteLayer(sourceLayer, layerName)
}

// CopyLayerToFile 复制图层到文件
func CopyLayerToFile(sourceLayer *GDALLayer, targetFilePath string, targetLayerName string, overwrite bool) error {
	return WriteGeospatialFile(sourceLayer, targetFilePath, targetLayerName, overwrite)
}

// ConvertFile 文件格式转换
func ConvertFile(sourceFilePath string, targetFilePath string, sourceLayerName string, targetLayerName string, overwrite bool) error {
	// 读取源文件
	sourceReader, err := NewFileGeoReader(sourceFilePath)
	if err != nil {
		return fmt.Errorf("无法读取源文件: %v", err)
	}

	sourceLayer, err := sourceReader.ReadLayer(sourceLayerName)
	if err != nil {
		return fmt.Errorf("无法读取源图层: %v", err)
	}
	defer sourceLayer.Close()

	// 写入目标文件
	err = WriteGeospatialFile(sourceLayer, targetFilePath, targetLayerName, overwrite)
	if err != nil {
		return fmt.Errorf("无法写入目标文件: %v", err)
	}

	return nil
}
