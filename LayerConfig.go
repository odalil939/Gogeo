/*
Copyright (C) 2024 [fmecool]

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
	"sync"
	"time"
	"unsafe"
)

// TileInfo 分块信息
type TileInfo struct {
	Index    int     // 分块索引
	MinX     float64 // 边界框
	MinY     float64
	MaxX     float64
	MaxY     float64
	Envelope C.OGRGeometryH // 分块包络几何体
}

// 修改分块结果结构，标记边界要素
type TileResult struct {
	TileIndex        int
	InteriorFeatures []C.OGRFeatureH // 完全在分块内部的要素
	BorderFeatures   []C.OGRFeatureH // 可能跨越边界的要素
	Error            error
	ProcessTime      time.Duration
}

// ParallelGeosConfig 并行相交分析配置
type ParallelGeosConfig struct {
	TileCount        int                      // 分块数量 (N*N)
	MaxWorkers       int                      // 最大工作协程数
	BufferDistance   float64                  // 分块缓冲距离
	IsMergeTile      bool                     // 是否合并瓦片
	ProgressCallback ProgressCallback         // 进度回调
	PrecisionConfig  *GeometryPrecisionConfig // 几何精度配置
}

// ProgressCallback 进度回调函数类型
// 返回值：true继续执行，false取消执行
type ProgressCallback func(complete float64, message string) bool

// ProgressData 进度数据结构，用于在C和Go之间传递信息
type ProgressData struct {
	callback  ProgressCallback
	cancelled bool
	mutex     sync.RWMutex
}

// 全局进度数据映射，用于在C回调中找到对应的Go回调
var (
	progressDataMap   = make(map[uintptr]*ProgressData)
	progressDataMutex sync.RWMutex
)

// handleProgressUpdate 处理来自C的进度更新
//
//export handleProgressUpdate
func handleProgressUpdate(complete C.double, message *C.char, progressArg unsafe.Pointer) C.int {
	// 线程安全地获取进度数据
	progressDataMutex.RLock()
	data, exists := progressDataMap[uintptr(progressArg)]
	progressDataMutex.RUnlock()

	if !exists || data == nil {
		return 1 // 继续执行
	}

	// 检查是否已被取消
	data.mutex.RLock()
	if data.cancelled {
		data.mutex.RUnlock()
		return 0 // 取消执行
	}
	callback := data.callback
	data.mutex.RUnlock()

	if callback != nil {
		// 转换消息字符串
		msg := ""
		if message != nil {
			msg = C.GoString(message)
		}

		// 调用Go回调函数
		shouldContinue := callback(float64(complete), msg)
		if !shouldContinue {
			// 用户取消操作
			data.mutex.Lock()
			data.cancelled = true
			data.mutex.Unlock()
			return 0 // 取消执行
		}
	}

	return 1 // 继续执行
}

// GeosAnalysisResult 相交分析结果
type GeosAnalysisResult struct {
	OutputLayer *GDALLayer
	ResultCount int
}

// FieldsInfo 字段信息结构
type FieldsInfo struct {
	Name      string
	Type      C.OGRFieldType
	FromTable string // 标记字段来源表
}

// FieldMergeStrategy 字段合并策略枚举
type FieldMergeStrategy int

const (
	// UseTable1Fields 只使用第一个表的字段
	UseTable1Fields FieldMergeStrategy = iota
	// UseTable2Fields 只使用第二个表的字段
	UseTable2Fields
	// MergePreferTable1 合并字段，冲突时优先使用table1
	MergePreferTable1
	// MergePreferTable2 合并字段，冲突时优先使用table2
	MergePreferTable2
	// MergeWithPrefix 合并字段，使用前缀区分来源
	MergeWithPrefix
)

func (s FieldMergeStrategy) String() string {
	switch s {
	case UseTable1Fields:
		return "只使用表1字段"
	case UseTable2Fields:
		return "只使用表2字段"
	case MergePreferTable1:
		return "合并字段(优先表1)"
	case MergePreferTable2:
		return "合并字段(优先表2)"
	case MergeWithPrefix:
		return "合并字段(使用前缀区分)"
	default:
		return "未知策略"
	}
}

// 获取要素几何体的标准化WKT表示，用于去重比较
func getFeatureGeometryWKT(feature C.OGRFeatureH) string {
	if feature == nil {
		return ""
	}

	geom := C.OGR_F_GetGeometryRef(feature)
	if geom == nil {
		return ""
	}

	// 创建几何体副本进行标准化处理
	geomClone := C.OGR_G_Clone(geom)
	if geomClone == nil {
		return ""
	}
	defer C.OGR_G_DestroyGeometry(geomClone)

	// 标准化几何体 - 这会统一坐标顺序和格式
	C.OGR_G_FlattenTo2D(geomClone) // 转为2D，去除Z坐标差异

	// 使用更精确的WKT导出，设置精度
	var wkt *C.char
	err := C.OGR_G_ExportToWkt(geomClone, &wkt)
	if err != C.OGRERR_NONE || wkt == nil {
		return ""
	}

	result := C.GoString(wkt)
	C.CPLFree(unsafe.Pointer(wkt))
	return result
}

// 添加去重后的边界要素
func addDeduplicatedBorderFeatures(borderFeaturesMap map[string]*BorderFeatureInfo, resultLayer *GDALLayer, progressCallback ProgressCallback) error {
	totalFeatures := len(borderFeaturesMap)
	addedCount := 0

	i := 0
	for _, info := range borderFeaturesMap {
		if info.Feature != nil {
			err := C.OGR_L_CreateFeature(resultLayer.layer, info.Feature)
			if err == C.OGRERR_NONE {
				addedCount++
			}
			C.OGR_F_Destroy(info.Feature)
			info.Feature = nil
		}

		i++

		// 更新进度
		if progressCallback != nil && i%100 == 0 {
			progress := 0.9 + 0.1*float64(i)/float64(totalFeatures)
			message := fmt.Sprintf("正在添加去重后的边界要素 %d/%d", i, totalFeatures)
			if !progressCallback(progress, message) {
				return fmt.Errorf("操作被用户取消")
			}
		}
	}

	fmt.Printf("成功添加边界要素 %d 个\n", addedCount)
	return nil
}

// 清理资源的辅助函数
func cleanupTileResult(result *TileResult) {
	for _, feature := range result.InteriorFeatures {
		if feature != nil {
			C.OGR_F_Destroy(feature)
		}
	}
	for _, feature := range result.BorderFeatures {
		if feature != nil {
			C.OGR_F_Destroy(feature)
		}
	}
}

func cleanupBorderFeaturesMap(borderFeaturesMap map[string]*BorderFeatureInfo) {
	for _, info := range borderFeaturesMap {
		if info.Feature != nil {
			C.OGR_F_Destroy(info.Feature)
		}
	}
}

// 添加图层字段到结果图层
func addLayerFields(resultLayer, sourceLayer *GDALLayer, prefix string) error {
	sourceDefn := C.OGR_L_GetLayerDefn(sourceLayer.layer)
	fieldCount := int(C.OGR_FD_GetFieldCount(sourceDefn))

	for i := 0; i < fieldCount; i++ {
		fieldDefn := C.OGR_FD_GetFieldDefn(sourceDefn, C.int(i))
		fieldName := C.OGR_Fld_GetNameRef(fieldDefn)
		fieldType := C.OGR_Fld_GetType(fieldDefn)

		// 构建字段名（可能带前缀）
		var newFieldName string
		if prefix != "" {
			newFieldName = prefix + C.GoString(fieldName)
		} else {
			newFieldName = C.GoString(fieldName)
		}

		// 创建新字段
		newFieldNameC := C.CString(newFieldName)
		newFieldDefn := C.OGR_Fld_Create(newFieldNameC, fieldType)

		// 复制字段属性
		C.OGR_Fld_SetWidth(newFieldDefn, C.OGR_Fld_GetWidth(fieldDefn))
		C.OGR_Fld_SetPrecision(newFieldDefn, C.OGR_Fld_GetPrecision(fieldDefn))

		// 添加字段到结果图层
		err := C.OGR_L_CreateField(resultLayer.layer, newFieldDefn, 1)

		// 清理资源
		C.OGR_Fld_Destroy(newFieldDefn)
		C.free(unsafe.Pointer(newFieldNameC))

		if err != C.OGRERR_NONE {
			return fmt.Errorf("创建字段 %s 失败，错误代码: %d", newFieldName, int(err))
		}
	}

	return nil
}

// 获取精度标志位
func (config *GeometryPrecisionConfig) getFlags() C.int {
	var flags C.int = 0

	if !config.PreserveTopo {
		flags |= C.OGR_GEOS_PREC_NO_TOPO
	}

	if config.KeepCollapsed {
		flags |= C.OGR_GEOS_PREC_KEEP_COLLAPSED
	}

	return flags
}

// BorderFeatureInfo 边界要素信息
type BorderFeatureInfo struct {
	Feature     C.OGRFeatureH
	TileIndices []int  // 该要素出现在哪些分块中
	GeometryWKT string // 几何体的WKT表示，用于去重比较
}
