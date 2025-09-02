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
static OGRErr performSymDifferenceWithProgress(OGRLayerH inputLayer,
                                     OGRLayerH methodLayer,
                                     OGRLayerH resultLayer,
                                     char **options,
                                     void *progressData) {
    return OGR_L_SymDifference(inputLayer, methodLayer, resultLayer, options,
                             progressCallback, progressData);
}



*/
import "C"
import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// TileClipInfo 瓦片裁剪信息
type TileClipInfo struct {
	Index int
	MinX  float64
	MinY  float64
	MaxX  float64
	MaxY  float64
}

// TileClipResult 瓦片裁剪结果
type TileClipResult struct {
	TileIndex   int
	Features    []C.OGRFeatureH
	ProcessTime time.Duration
	Error       error
}

func SpatialSymDifferenceAnalysis(layer1, layer2 *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error) {

	defer layer1.Close()

	defer layer2.Close()
	table1 := layer1.GetLayerName()
	table2 := layer2.GetLayerName()
	// 为两个图层添加唯一标识字段（这会创建新的内存图层）
	err := addUniqueIdentifierFields(layer1, layer2, table1, table2)
	if err != nil {
		return nil, fmt.Errorf("添加唯一标识字段失败: %v", err)
	}

	// 执行基于瓦片裁剪的并行对称差异分析
	resultLayer, err := performTileClipSymDifferenceAnalysis(layer1, layer2, table1, table2, config)
	if err != nil {
		return nil, fmt.Errorf("执行瓦片裁剪对称差异分析失败: %v", err)
	}

	// 计算结果数量
	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪并行分块数: %dx%d\n", config.TileCount, config.TileCount)
	fmt.Printf("对称差异分析完成，共生成 %d 个要素\n", resultCount)
	if config.IsMergeTile {
		// 执行按标识字段的融合操作
		unionResult, err := performUnionByIdentifierFields(resultLayer, table1, table2, config.PrecisionConfig, config.ProgressCallback)
		if err != nil {
			return nil, fmt.Errorf("执行融合操作失败: %v", err)
		}

		fmt.Printf("融合操作完成，最终生成 %d 个要素\n", unionResult.ResultCount)
		// 删除临时的_symDifferenceID字段
		err = removeTempIdentifierFields(unionResult.OutputLayer, table1, table2)
		if err != nil {
			fmt.Printf("警告: 删除临时标识字段失败: %v\n", err)
			// 不返回错误，因为这不是关键操作
		} else {
			fmt.Printf("成功删除临时标识字段: %s_symDifferenceID, %s_symDifferenceID\n", table1, table2)
		}

		return unionResult, nil
	} else {
		return &GeosAnalysisResult{
			OutputLayer: resultLayer,
			ResultCount: resultCount,
		}, nil
	}

}

// removeTempIdentifierFields 删除临时的_symDifferenceID字段
func removeTempIdentifierFields(resultLayer *GDALLayer, table1Name, table2Name string) error {
	field1Name := table1Name + "_symDifferenceID"
	field2Name := table2Name + "_symDifferenceID"

	err1 := deleteFieldFromLayer(resultLayer, field1Name)
	if err1 != nil {
		return fmt.Errorf("删除字段 %s 失败: %v", field1Name, err1)
	}

	err2 := deleteFieldFromLayer(resultLayer, field2Name)
	if err2 != nil {
		return fmt.Errorf("删除字段 %s 失败: %v", field2Name, err2)
	}

	return nil
}

// deleteFieldFromLayer 从图层中删除指定字段
func deleteFieldFromLayer(layer *GDALLayer, fieldName string) error {
	layerDefn := C.OGR_L_GetLayerDefn(layer.layer)
	fieldNameC := C.CString(fieldName)
	defer C.free(unsafe.Pointer(fieldNameC))

	fieldIndex := C.OGR_FD_GetFieldIndex(layerDefn, fieldNameC)
	if fieldIndex < 0 {
		fmt.Printf("字段 %s 不存在，跳过删除\n", fieldName)
		return nil
	}

	err := C.OGR_L_DeleteField(layer.layer, fieldIndex)
	if err != C.OGRERR_NONE {
		return fmt.Errorf("删除字段失败，错误代码: %d", int(err))
	}

	return nil
}

// 修改 addUniqueIdentifierFields 函数
func addUniqueIdentifierFields(layer1, layer2 *GDALLayer, table1Name, table2Name string) error {
	// 为第一个图层创建带标识字段的副本
	newLayer1, err := createLayerWithIdentifierField(layer1, table1Name+"_symDifferenceID")
	if err != nil {
		return fmt.Errorf("为图层 %s 创建带标识字段的副本失败: %v", table1Name, err)
	}

	// 为第二个图层创建带标识字段的副本
	newLayer2, err := createLayerWithIdentifierField(layer2, table2Name+"_symDifferenceID")
	if err != nil {
		return fmt.Errorf("为图层 %s 创建带标识字段的副本失败: %v", table2Name, err)
	}

	// 替换原始图层
	layer1.Close()
	layer2.Close()
	*layer1 = *newLayer1
	*layer2 = *newLayer2

	fmt.Printf("成功添加标识字段: %s_symDifferenceID, %s_symDifferenceID\n", table1Name, table2Name)
	return nil
}

// createLayerWithIdentifierField 创建一个带有标识字段的新图层
func createLayerWithIdentifierField(sourceLayer *GDALLayer, identifierFieldName string) (*GDALLayer, error) {
	// 创建内存数据源
	driverName := C.CString("MEM")
	driver := C.OGRGetDriverByName(driverName)
	C.free(unsafe.Pointer(driverName))

	if driver == nil {
		return nil, fmt.Errorf("无法获取Memory驱动")
	}

	dataSourceName := C.CString("")
	dataSource := C.OGR_Dr_CreateDataSource(driver, dataSourceName, nil)
	C.free(unsafe.Pointer(dataSourceName))

	if dataSource == nil {
		return nil, fmt.Errorf("无法创建内存数据源")
	}

	// 获取源图层的空间参考系统
	sourceLayerDefn := C.OGR_L_GetLayerDefn(sourceLayer.layer)
	sourceSRS := C.OGR_L_GetSpatialRef(sourceLayer.layer)

	// 创建新图层
	layerName := C.CString("temp_layer")
	geomType := C.OGR_FD_GetGeomType(sourceLayerDefn)
	newLayer := C.OGR_DS_CreateLayer(dataSource, layerName, sourceSRS, geomType, nil)
	C.free(unsafe.Pointer(layerName))

	if newLayer == nil {
		C.OGR_DS_Destroy(dataSource)
		return nil, fmt.Errorf("无法创建新图层")
	}

	// 复制原有字段定义
	fieldCount := C.OGR_FD_GetFieldCount(sourceLayerDefn)
	for i := C.int(0); i < fieldCount; i++ {
		fieldDefn := C.OGR_FD_GetFieldDefn(sourceLayerDefn, i)
		if C.OGR_L_CreateField(newLayer, fieldDefn, 1) != C.OGRERR_NONE {
			C.OGR_DS_Destroy(dataSource)
			return nil, fmt.Errorf("复制字段定义失败")
		}
	}

	// 添加标识字段
	identifierFieldNameC := C.CString(identifierFieldName)
	identifierFieldDefn := C.OGR_Fld_Create(identifierFieldNameC, C.OFTInteger64)
	defer C.OGR_Fld_Destroy(identifierFieldDefn)
	defer C.free(unsafe.Pointer(identifierFieldNameC))

	if C.OGR_L_CreateField(newLayer, identifierFieldDefn, 1) != C.OGRERR_NONE {
		C.OGR_DS_Destroy(dataSource)
		return nil, fmt.Errorf("创建标识字段失败")
	}

	// 获取标识字段索引
	newLayerDefn := C.OGR_L_GetLayerDefn(newLayer)
	identifierFieldIndex := C.OGR_FD_GetFieldIndex(newLayerDefn, identifierFieldNameC)

	// 复制要素并添加标识字段值
	sourceLayer.ResetReading()
	var featureID int64 = 1

	sourceLayer.IterateFeatures(func(sourceFeature C.OGRFeatureH) {
		// 创建新要素
		newFeature := C.OGR_F_Create(newLayerDefn)
		if newFeature == nil {
			return
		}
		defer C.OGR_F_Destroy(newFeature)

		// 复制几何体
		geom := C.OGR_F_GetGeometryRef(sourceFeature)
		if geom != nil {
			geomClone := C.OGR_G_Clone(geom)
			C.OGR_F_SetGeometry(newFeature, geomClone)
			C.OGR_G_DestroyGeometry(geomClone)
		}

		// 复制原有字段值
		for i := C.int(0); i < fieldCount; i++ {
			if C.OGR_F_IsFieldSet(sourceFeature, i) != 0 {
				sourceFieldDefn := C.OGR_FD_GetFieldDefn(sourceLayerDefn, i)
				fieldType := C.OGR_Fld_GetType(sourceFieldDefn)

				switch fieldType {
				case C.OFTInteger:
					value := C.OGR_F_GetFieldAsInteger(sourceFeature, i)
					C.OGR_F_SetFieldInteger(newFeature, i, value)
				case C.OFTInteger64:
					value := C.OGR_F_GetFieldAsInteger64(sourceFeature, i)
					C.OGR_F_SetFieldInteger64(newFeature, i, value)
				case C.OFTReal:
					value := C.OGR_F_GetFieldAsDouble(sourceFeature, i)
					C.OGR_F_SetFieldDouble(newFeature, i, value)
				case C.OFTString:
					value := C.OGR_F_GetFieldAsString(sourceFeature, i)
					C.OGR_F_SetFieldString(newFeature, i, value)
				case C.OFTDate, C.OFTTime, C.OFTDateTime:
					var year, month, day, hour, minute, second, tzflag C.int
					C.OGR_F_GetFieldAsDateTime(sourceFeature, i, &year, &month, &day, &hour, &minute, &second, &tzflag)
					C.OGR_F_SetFieldDateTime(newFeature, i, year, month, day, hour, minute, second, tzflag)
				default:
					value := C.OGR_F_GetFieldAsString(sourceFeature, i)
					C.OGR_F_SetFieldString(newFeature, i, value)
				}
			}
		}

		// 设置标识字段值
		C.OGR_F_SetFieldInteger64(newFeature, identifierFieldIndex, C.longlong(featureID))
		featureID++

		// 添加要素到新图层
		C.OGR_L_CreateFeature(newLayer, newFeature)
	})

	// 创建新的GDALLayer包装器
	result := &GDALLayer{
		layer:   newLayer,
		dataset: dataSource,
	}

	fmt.Printf("成功创建带标识字段的图层，共处理 %d 个要素\n", featureID-1)
	return result, nil
}

// performUnionByIdentifierFields 执行按标识字段的融合操作
func performUnionByIdentifierFields(inputLayer *GDALLayer, table1Name, table2Name string,
	precisionConfig *GeometryPrecisionConfig, progressCallback ProgressCallback) (*GeosAnalysisResult, error) {

	// 构建分组字段列表
	groupFields := []string{
		SourceIdentifierField,           // "source_layer" 字段
		table1Name + "_symDifferenceID", // 第一个表的标识字段
		table2Name + "_symDifferenceID", // 第二个表的标识字段
	}

	// 构建输出图层名称
	outputLayerName := fmt.Sprintf("symdiff_union_%s_%s", table1Name, table2Name)

	// 为融合操作调整精度配置（放大网格大小）
	fusionPrecisionConfig := *precisionConfig
	fusionPrecisionConfig.GridSize = 0

	// 执行融合操作
	return UnionByFieldsWithPrecision(inputLayer, groupFields, outputLayerName, &fusionPrecisionConfig, progressCallback)
}

// performTileClipSymDifferenceAnalysis 执行基于瓦片裁剪的并行对称差异分析
func performTileClipSymDifferenceAnalysis(layer1, layer2 *GDALLayer, table1Name, table2Name string, config *ParallelGeosConfig) (*GDALLayer, error) {
	// 在分块裁剪前对两个图层进行精度处理
	if config.PrecisionConfig != nil && config.PrecisionConfig.Enabled && config.PrecisionConfig.GridSize > 0 {
		fmt.Printf("开始对图层进行精度处理，网格大小: %f\n", config.PrecisionConfig.GridSize)

		flags := config.PrecisionConfig.getFlags()
		gridSize := C.double(config.PrecisionConfig.GridSize)

		// 对第一个图层进行精度处理
		C.setLayerGeometryPrecision(layer1.layer, gridSize, flags)
		fmt.Printf("图层 %s 精度处理完成\n", table1Name)

		// 对第二个图层进行精度处理
		C.setLayerGeometryPrecision(layer2.layer, gridSize, flags)
		fmt.Printf("图层 %s 精度处理完成\n", table2Name)
	}

	// 获取数据范围
	extent, err := getLayersExtent(layer1, layer2)
	if err != nil {
		return nil, fmt.Errorf("获取数据范围失败: %v", err)
	}

	// 创建瓦片裁剪信息（不使用缓冲区）
	tiles := createTileClipInfos(extent, config.TileCount)
	fmt.Printf("创建了 %d 个瓦片进行裁剪并行处理\n", len(tiles))

	// 创建结果图层
	resultLayer, err := createGeosAnalysisResultLayer(layer1, layer2, table1Name, table2Name)
	if err != nil {
		return nil, fmt.Errorf("创建结果图层失败: %v", err)
	}

	// 为每个工作线程创建图层副本
	layer1Copies, layer2Copies, err := createLayerCopies(layer1, layer2, config.MaxWorkers)
	if err != nil {
		return nil, fmt.Errorf("创建图层副本失败: %v", err)
	}
	defer cleanupLayerCopies(layer1Copies, layer2Copies)

	// 并行处理瓦片裁剪（不再传递精度配置，因为已经在分块前处理过了）
	err = processTileClipSymDifferenceInParallel(layer1Copies, layer2Copies, resultLayer, tiles, table1Name, table2Name, config)
	if err != nil {
		return nil, fmt.Errorf("并行处理瓦片裁剪失败: %v", err)
	}

	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪对称差异分析完成，共生成 %d 个要素\n", resultCount)

	return resultLayer, nil
}

// createTileClipInfos 创建瓦片裁剪信息
func createTileClipInfos(extent *Extent, tileCount int) []*TileClipInfo {
	tiles := make([]*TileClipInfo, 0, tileCount*tileCount)

	width := extent.MaxX - extent.MinX
	height := extent.MaxY - extent.MinY

	tileWidth := width / float64(tileCount)
	tileHeight := height / float64(tileCount)

	index := 0
	for row := 0; row < tileCount; row++ {
		for col := 0; col < tileCount; col++ {
			minX := extent.MinX + float64(col)*tileWidth
			maxX := extent.MinX + float64(col+1)*tileWidth
			minY := extent.MinY + float64(row)*tileHeight
			maxY := extent.MinY + float64(row+1)*tileHeight

			// 确保最后一行/列覆盖到边界
			if col == tileCount-1 {
				maxX = extent.MaxX
			}
			if row == tileCount-1 {
				maxY = extent.MaxY
			}

			tiles = append(tiles, &TileClipInfo{
				Index: index,
				MinX:  minX,
				MinY:  minY,
				MaxX:  maxX,
				MaxY:  maxY,
			})
			index++
		}
	}

	return tiles
}

// processTileClipSymDifferenceInParallel 并行处理瓦片裁剪对称差异
func processTileClipSymDifferenceInParallel(layer1Copies, layer2Copies []*GDALLayer, resultLayer *GDALLayer, tiles []*TileClipInfo, table1Name, table2Name string, config *ParallelGeosConfig) error {
	// 创建工作池
	tilesChan := make(chan *TileClipInfo, len(tiles))
	resultsChan := make(chan *TileClipResult, len(tiles))

	// 启动工作协程
	var wg sync.WaitGroup
	for i := 0; i < config.MaxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			processTileClipSymDifferenceWorker(workerID, layer1Copies[workerID], layer2Copies[workerID], table1Name, table2Name, config.PrecisionConfig, tilesChan, resultsChan)
		}(i)
	}

	// 发送分块任务
	go func() {
		defer close(tilesChan)
		for _, tile := range tiles {
			tilesChan <- tile
		}
	}()

	// 收集结果
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// 收集并合并结果（无需去重，每个瓦片结果互不重叠）
	return collectTileClipResults(resultsChan, resultLayer, len(tiles), config.ProgressCallback)
}

// processTileClipSymDifferenceWorker 瓦片裁剪对称差异工作协程
func processTileClipSymDifferenceWorker(workerID int, layer1Copy, layer2Copy *GDALLayer, table1Name, table2Name string, precisionConfig *GeometryPrecisionConfig, tilesChan <-chan *TileClipInfo, resultsChan chan<- *TileClipResult) {
	for tile := range tilesChan {
		startTime := time.Now()

		result := &TileClipResult{
			TileIndex: tile.Index,
			Features:  make([]C.OGRFeatureH, 0),
		}

		// 处理单个瓦片的裁剪对称差异
		features, err := processSingleTileClipSymDifference(layer1Copy, layer2Copy, tile, workerID, table1Name, table2Name, precisionConfig)
		if err != nil {
			result.Error = fmt.Errorf("工作协程 %d 处理瓦片 %d 失败: %v", workerID, tile.Index, err)
		} else {
			result.Features = features
		}

		result.ProcessTime = time.Since(startTime)
		resultsChan <- result
	}
}

// processSingleTileClipSymDifference 处理单个瓦片的裁剪对称差异
func processSingleTileClipSymDifference(
	layer1Copy, layer2Copy *GDALLayer, tile *TileClipInfo, workerID int, table1Name, table2Name string, precisionConfig *GeometryPrecisionConfig) (
	[]C.OGRFeatureH, error) {

	// 创建瓦片裁剪后的图层
	layer1Name := C.CString(fmt.Sprintf("clipped1_worker%d_tile%d", workerID, tile.Index))
	layer2Name := C.CString(fmt.Sprintf("clipped2_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(layer1Name))
	defer C.free(unsafe.Pointer(layer2Name))

	// 裁剪两个图层到当前瓦片范围
	table1NameC := C.CString(table1Name)
	table2NameC := C.CString(table2Name)
	defer C.free(unsafe.Pointer(table1NameC))
	defer C.free(unsafe.Pointer(table2NameC))

	clippedLayer1Ptr := C.clipLayerToTile(layer1Copy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), layer1Name, table1NameC)

	clippedLayer2Ptr := C.clipLayerToTile(layer2Copy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), layer2Name, table2NameC)

	if clippedLayer1Ptr == nil || clippedLayer2Ptr == nil {
		if clippedLayer1Ptr != nil {
			C.OGR_L_Dereference(clippedLayer1Ptr)
		}
		if clippedLayer2Ptr != nil {
			C.OGR_L_Dereference(clippedLayer2Ptr)
		}
		return nil, fmt.Errorf("裁剪图层到瓦片失败")
	}
	defer C.OGR_L_Dereference(clippedLayer1Ptr)
	defer C.OGR_L_Dereference(clippedLayer2Ptr)

	clippedLayer1 := &GDALLayer{layer: clippedLayer1Ptr}
	clippedLayer2 := &GDALLayer{layer: clippedLayer2Ptr}

	// 检查裁剪后的图层是否有要素
	count1 := clippedLayer1.GetFeatureCount()
	count2 := clippedLayer2.GetFeatureCount()

	if count1 == 0 && count2 == 0 {
		return []C.OGRFeatureH{}, nil
	}

	// 创建结果临时图层
	resultTempName := C.CString(fmt.Sprintf("symdiff_result_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(resultTempName))

	srs := clippedLayer1.GetSpatialRef()
	if srs == nil {
		srs = clippedLayer2.GetSpatialRef()
	}

	resultTempPtr := C.createMemoryLayer(resultTempName, C.wkbMultiPolygon, srs)
	if resultTempPtr == nil {
		return nil, fmt.Errorf("创建结果临时图层失败")
	}
	defer C.OGR_L_Dereference(resultTempPtr)

	resultTemp := &GDALLayer{layer: resultTempPtr}

	// 添加字段定义
	err := addSymDifferenceFields(resultTemp, clippedLayer1, clippedLayer2, table1Name, table2Name)
	if err != nil {
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	// 执行对称差异分析
	err = executeSymDifferenceAnalysis(clippedLayer1, clippedLayer2, resultTemp, table1Name, table2Name, nil)
	if err != nil {
		return nil, fmt.Errorf("瓦片对称差异分析失败: %v", err)
	}

	// 收集结果要素
	features := make([]C.OGRFeatureH, 0)
	resultTemp.ResetReading()

	resultTemp.IterateFeatures(func(feature C.OGRFeatureH) {
		clonedFeature := C.OGR_F_Clone(feature)
		if clonedFeature != nil {
			features = append(features, clonedFeature)
		}
	})

	return features, nil
}

// collectTileClipResults 收集瓦片裁剪结果（无需去重）
func collectTileClipResults(resultsChan <-chan *TileClipResult, resultLayer *GDALLayer, totalTiles int, progressCallback ProgressCallback) error {
	processedTiles := 0
	var totalProcessTime time.Duration
	totalFeatures := 0

	resultMutex := sync.Mutex{}

	// 收集所有结果
	for result := range resultsChan {
		if result.Error != nil {
			// 清理资源
			for _, feature := range result.Features {
				if feature != nil {
					C.OGR_F_Destroy(feature)
				}
			}
			return result.Error
		}

		resultMutex.Lock()

		// 直接添加所有要素（瓦片间无重叠，无需去重）
		for _, feature := range result.Features {
			if feature != nil {
				C.OGR_L_CreateFeature(resultLayer.layer, feature)
				C.OGR_F_Destroy(feature)
			}
		}

		resultMutex.Unlock()

		processedTiles++
		totalProcessTime += result.ProcessTime
		totalFeatures += len(result.Features)

		// 更新进度
		if progressCallback != nil {
			progress := float64(processedTiles) / float64(totalTiles)
			message := fmt.Sprintf("已完成瓦片 %d/%d，累计生成要素 %d 个",
				processedTiles, totalTiles, totalFeatures)

			if !progressCallback(progress, message) {
				return fmt.Errorf("操作被用户取消")
			}
		}
	}

	fmt.Printf("瓦片裁剪结果汇总完成: 处理 %d 个瓦片，生成 %d 个要素\n",
		processedTiles, totalFeatures)
	fmt.Printf("总处理时间: %v，平均每瓦片: %v\n",
		totalProcessTime, totalProcessTime/time.Duration(totalTiles))

	return nil
}

// createGeosAnalysisResultLayer 创建对称差异结果图层
func createGeosAnalysisResultLayer(layer1, layer2 *GDALLayer, table1Name, table2Name string) (*GDALLayer, error) {
	layerName := C.CString("symdifference_result")
	defer C.free(unsafe.Pointer(layerName))

	// 获取空间参考系统
	srs := layer1.GetSpatialRef()
	if srs == nil {
		srs = layer2.GetSpatialRef()
	}

	// 创建结果图层
	resultLayerPtr := C.createMemoryLayer(layerName, C.wkbMultiPolygon, srs)
	if resultLayerPtr == nil {
		return nil, fmt.Errorf("创建结果图层失败")
	}

	resultLayer := &GDALLayer{layer: resultLayerPtr}
	runtime.SetFinalizer(resultLayer, (*GDALLayer).cleanup)

	// 添加字段定义 - 使用默认策略（合并字段，带前缀区分来源）
	err := addSymDifferenceFields(resultLayer, layer1, layer2, table1Name, table2Name)
	if err != nil {
		resultLayer.Close()
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	return resultLayer, nil
}

// SourceIdentifierField 来源标识字段名
const SourceIdentifierField = "source_layer"

// addSymDifferenceFields 添加对称差异分析的字段（修改版本）
func addSymDifferenceFields(resultLayer, layer1, layer2 *GDALLayer, table1Name, table2Name string) error {
	// 首先添加来源标识字段
	sourceFieldName := C.CString(SourceIdentifierField)
	sourceFieldDefn := C.OGR_Fld_Create(sourceFieldName, C.OFTString)
	C.OGR_Fld_SetWidth(sourceFieldDefn, 50) // 设置字段宽度
	err := C.OGR_L_CreateField(resultLayer.layer, sourceFieldDefn, 1)
	C.OGR_Fld_Destroy(sourceFieldDefn)
	C.free(unsafe.Pointer(sourceFieldName))

	if err != C.OGRERR_NONE {
		return fmt.Errorf("创建来源标识字段失败，错误代码: %d", int(err))
	}

	// 合并两个图层的字段（不使用前缀）
	err1 := addLayerFieldsWithoutPrefix(resultLayer, layer1)
	if err1 != nil {
		return fmt.Errorf("添加图层1字段失败: %v", err1)
	}

	err2 := addLayerFieldsWithoutPrefix(resultLayer, layer2)
	if err2 != nil {
		return fmt.Errorf("添加图层2字段失败: %v", err2)
	}

	return nil
}

// addLayerFieldsWithoutPrefix 添加图层字段到结果图层（不使用前缀，处理重名字段）
func addLayerFieldsWithoutPrefix(resultLayer, sourceLayer *GDALLayer) error {
	sourceDefn := C.OGR_L_GetLayerDefn(sourceLayer.layer)
	resultDefn := C.OGR_L_GetLayerDefn(resultLayer.layer)
	fieldCount := int(C.OGR_FD_GetFieldCount(sourceDefn))

	for i := 0; i < fieldCount; i++ {
		fieldDefn := C.OGR_FD_GetFieldDefn(sourceDefn, C.int(i))
		fieldName := C.GoString(C.OGR_Fld_GetNameRef(fieldDefn))
		fieldType := C.OGR_Fld_GetType(fieldDefn)

		// 检查结果图层中是否已存在同名字段
		existingFieldIndex := C.OGR_FD_GetFieldIndex(resultDefn, C.CString(fieldName))
		if existingFieldIndex >= 0 {
			// 字段已存在，检查类型是否兼容
			existingFieldDefn := C.OGR_FD_GetFieldDefn(resultDefn, existingFieldIndex)
			existingFieldType := C.OGR_Fld_GetType(existingFieldDefn)

			if existingFieldType != fieldType {
				// 类型不兼容，跳过或者可以选择转换为字符串类型
				fmt.Printf("警告: 字段 %s 类型不兼容，跳过添加\n", fieldName)
				continue
			}
			// 类型兼容，跳过添加（字段已存在）
			continue
		}

		// 创建新字段
		fieldNameC := C.CString(fieldName)
		newFieldDefn := C.OGR_Fld_Create(fieldNameC, fieldType)

		// 复制字段属性
		C.OGR_Fld_SetWidth(newFieldDefn, C.OGR_Fld_GetWidth(fieldDefn))
		C.OGR_Fld_SetPrecision(newFieldDefn, C.OGR_Fld_GetPrecision(fieldDefn))

		// 添加字段到结果图层
		err := C.OGR_L_CreateField(resultLayer.layer, newFieldDefn, 1)

		// 清理资源
		C.OGR_Fld_Destroy(newFieldDefn)
		C.free(unsafe.Pointer(fieldNameC))

		if err != C.OGRERR_NONE {
			return fmt.Errorf("创建字段 %s 失败，错误代码: %d", fieldName, int(err))
		}
	}

	return nil
}

// executeSymDifferenceAnalysis 执行对称差异分析
func executeSymDifferenceAnalysis(layer1, layer2, resultLayer *GDALLayer, table1Name, table2Name string, progressCallback ProgressCallback) error {
	// 设置GDAL选项
	var options **C.char
	defer func() {
		if options != nil {
			C.CSLDestroy(options)
		}
	}()

	skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
	promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
	keepLowerDimOpt := C.CString("KEEP_LOWER_DIMENSION_GEOMETRIES=NO")
	defer C.free(unsafe.Pointer(skipFailuresOpt))
	defer C.free(unsafe.Pointer(promoteToMultiOpt))
	defer C.free(unsafe.Pointer(keepLowerDimOpt))

	options = C.CSLAddString(options, skipFailuresOpt)
	options = C.CSLAddString(options, promoteToMultiOpt)
	options = C.CSLAddString(options, keepLowerDimOpt)

	// 执行对称差异操作
	return executeGDALSymDifferenceWithProgress(layer1, layer2, resultLayer, options, progressCallback)
}

// executeGDALSymDifferenceWithProgress 执行带进度的GDAL对称差异操作
func executeGDALSymDifferenceWithProgress(layer1, layer2, resultLayer *GDALLayer, options **C.char, progressCallback ProgressCallback) error {
	var progressData *ProgressData
	var progressArg unsafe.Pointer

	// 设置进度回调
	if progressCallback != nil {
		progressData = &ProgressData{
			callback:  progressCallback,
			cancelled: false,
		}
		progressArg = unsafe.Pointer(uintptr(unsafe.Pointer(progressData)))

		progressDataMutex.Lock()
		progressDataMap[uintptr(progressArg)] = progressData
		progressDataMutex.Unlock()

		defer func() {
			progressDataMutex.Lock()
			delete(progressDataMap, uintptr(progressArg))
			progressDataMutex.Unlock()
		}()
	}

	// 调用GDAL的对称差异函数
	var err C.OGRErr
	if progressCallback != nil {
		err = C.performSymDifferenceWithProgress(layer1.layer, layer2.layer, resultLayer.layer, options, progressArg)
	} else {
		err = C.OGR_L_SymDifference(layer1.layer, layer2.layer, resultLayer.layer, options, nil, nil)
	}

	if err != C.OGRERR_NONE {
		return fmt.Errorf("GDAL对称差异操作失败，错误代码: %d", int(err))
	}

	return nil
}
