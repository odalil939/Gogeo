package Gogeo

/*
#include "osgeo_utils.h"
static OGRErr performUpdateWithProgress(OGRLayerH inputLayer,
                                       OGRLayerH updateLayer,
                                       OGRLayerH resultLayer,
                                       char **options,
                                       void *progressData) {
    return OGR_L_Update(inputLayer, updateLayer, resultLayer, options,
                       progressCallback, progressData);
}

// 修改 clipLayerToTile 函数，添加来源标识参数（复用原有函数）
*/
import "C"
import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// 修改 addUniqueIdentifierFields 函数
func addUniqueIdentifierFieldsByUpdate(layer1, layer2 *GDALLayer, table1Name, table2Name string) error {
	// 为第一个图层创建带标识字段的副本
	newLayer1, err := createLayerWithIdentifierField(layer1, table1Name+"_updateID")
	if err != nil {
		return fmt.Errorf("为图层 %s 创建带标识字段的副本失败: %v", table1Name, err)
	}

	// 为第二个图层创建带标识字段的副本
	newLayer2, err := createLayerWithIdentifierField(layer2, table2Name+"_updateID")
	if err != nil {
		return fmt.Errorf("为图层 %s 创建带标识字段的副本失败: %v", table2Name, err)
	}

	// 替换原始图层
	layer1.Close()
	layer2.Close()
	*layer1 = *newLayer1
	*layer2 = *newLayer2

	return nil
}

// SpatialUpdateAnalysisParallel 执行并行空间更新分析
func SpatialUpdateAnalysis(inputLayer, updateLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error) {

	defer inputLayer.Close()

	defer updateLayer.Close()
	inputTable := inputLayer.GetLayerName()
	updateTable := inputLayer.GetLayerName()
	// 为两个图层添加唯一标识字段
	err := addUniqueIdentifierFieldsByUpdate(inputLayer, updateLayer, inputTable, updateTable)
	if err != nil {
		return nil, fmt.Errorf("添加唯一标识字段失败: %v", err)
	}

	// 执行基于瓦片裁剪的并行更新分析
	resultLayer, err := performTileClipUpdateAnalysis(inputLayer, updateLayer, inputTable, updateTable, config)
	if err != nil {
		return nil, fmt.Errorf("执行瓦片裁剪更新分析失败: %v", err)
	}

	// 计算结果数量
	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪并行分块数: %dx%d\n", config.TileCount, config.TileCount)
	fmt.Printf("更新分析完成，共生成 %d 个要素\n", resultCount)
	if config.IsMergeTile {
		// 执行按标识字段的融合操作
		unionResult, err := performUnionByIdentifierFieldsForUpdate(resultLayer, inputTable, updateTable, config.PrecisionConfig, config.ProgressCallback)
		if err != nil {
			return nil, fmt.Errorf("执行融合操作失败: %v", err)
		}

		fmt.Printf("融合操作完成，最终生成 %d 个要素\n", unionResult.ResultCount)

		// 删除临时的_updateID字段
		err = removeTempIdentifierFieldsForUpdate(unionResult.OutputLayer, inputTable, updateTable)
		if err != nil {
			fmt.Printf("警告: 删除临时标识字段失败: %v\n", err)
		} else {
			fmt.Printf("成功删除临时标识字段: %s_updateID, %s_updateID\n", inputTable, updateTable)
		}

		return unionResult, nil
	} else {
		return &GeosAnalysisResult{
			OutputLayer: resultLayer,
			ResultCount: resultCount,
		}, nil
	}

}

// removeTempIdentifierFieldsForUpdate 删除临时的_updateID字段
func removeTempIdentifierFieldsForUpdate(resultLayer *GDALLayer, inputTableName, updateTableName string) error {
	field1Name := inputTableName + "_updateID"
	field2Name := updateTableName + "_updateID"

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

// performUnionByIdentifierFieldsForUpdate 执行按标识字段的融合操作（更新版本）
func performUnionByIdentifierFieldsForUpdate(inputLayer *GDALLayer, inputTableName, updateTableName string,
	precisionConfig *GeometryPrecisionConfig, progressCallback ProgressCallback) (*GeosAnalysisResult, error) {

	// 构建分组字段列表
	groupFields := []string{
		SourceIdentifierField,         // "source_layer" 字段
		inputTableName + "_updateID",  // 输入表的标识字段
		updateTableName + "_updateID", // 更新表的标识字段
	}

	// 构建输出图层名称
	outputLayerName := fmt.Sprintf("update_union_%s_%s", inputTableName, updateTableName)

	// 为融合操作调整精度配置
	fusionPrecisionConfig := *precisionConfig
	fusionPrecisionConfig.GridSize = 0

	// 执行融合操作
	return UnionByFieldsWithPrecision(inputLayer, groupFields, outputLayerName, &fusionPrecisionConfig, progressCallback)
}

// performTileClipUpdateAnalysis 执行基于瓦片裁剪的并行更新分析
func performTileClipUpdateAnalysis(inputLayer, updateLayer *GDALLayer, inputTableName, updateTableName string, config *ParallelGeosConfig) (*GDALLayer, error) {
	// 在分块裁剪前对两个图层进行精度处理
	if config.PrecisionConfig != nil && config.PrecisionConfig.Enabled && config.PrecisionConfig.GridSize > 0 {
		fmt.Printf("开始对图层进行精度处理，网格大小: %f\n", config.PrecisionConfig.GridSize)

		flags := config.PrecisionConfig.getFlags()
		gridSize := C.double(config.PrecisionConfig.GridSize)

		// 对输入图层进行精度处理
		C.setLayerGeometryPrecision(inputLayer.layer, gridSize, flags)
		fmt.Printf("输入图层 %s 精度处理完成\n", inputTableName)

		// 对更新图层进行精度处理
		C.setLayerGeometryPrecision(updateLayer.layer, gridSize, flags)
		fmt.Printf("更新图层 %s 精度处理完成\n", updateTableName)
	}

	// 获取数据范围
	extent, err := getLayersExtent(inputLayer, updateLayer)
	if err != nil {
		return nil, fmt.Errorf("获取数据范围失败: %v", err)
	}

	// 创建瓦片裁剪信息
	tiles := createTileClipInfos(extent, config.TileCount)
	fmt.Printf("创建了 %d 个瓦片进行裁剪并行处理\n", len(tiles))

	// 创建结果图层
	resultLayer, err := createUpdateAnalysisResultLayer(inputLayer, updateLayer, inputTableName, updateTableName)
	if err != nil {
		return nil, fmt.Errorf("创建结果图层失败: %v", err)
	}

	// 为每个工作线程创建图层副本
	inputLayerCopies, updateLayerCopies, err := createLayerCopies(inputLayer, updateLayer, config.MaxWorkers)
	if err != nil {
		return nil, fmt.Errorf("创建图层副本失败: %v", err)
	}
	defer cleanupLayerCopies(inputLayerCopies, updateLayerCopies)

	// 并行处理瓦片裁剪
	err = processTileClipUpdateInParallel(inputLayerCopies, updateLayerCopies, resultLayer, tiles, inputTableName, updateTableName, config)
	if err != nil {
		return nil, fmt.Errorf("并行处理瓦片裁剪失败: %v", err)
	}

	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪更新分析完成，共生成 %d 个要素\n", resultCount)

	return resultLayer, nil
}

// processTileClipUpdateInParallel 并行处理瓦片裁剪更新
func processTileClipUpdateInParallel(inputLayerCopies, updateLayerCopies []*GDALLayer, resultLayer *GDALLayer, tiles []*TileClipInfo, inputTableName, updateTableName string, config *ParallelGeosConfig) error {
	// 创建工作池
	tilesChan := make(chan *TileClipInfo, len(tiles))
	resultsChan := make(chan *TileClipResult, len(tiles))

	// 启动工作协程
	var wg sync.WaitGroup
	for i := 0; i < config.MaxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			processTileClipUpdateWorker(workerID, inputLayerCopies[workerID], updateLayerCopies[workerID], inputTableName, updateTableName, config.PrecisionConfig, tilesChan, resultsChan)
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

	// 收集并合并结果
	return collectTileClipResults(resultsChan, resultLayer, len(tiles), config.ProgressCallback)
}

// processTileClipUpdateWorker 瓦片裁剪更新工作协程
func processTileClipUpdateWorker(workerID int, inputLayerCopy, updateLayerCopy *GDALLayer, inputTableName, updateTableName string, precisionConfig *GeometryPrecisionConfig, tilesChan <-chan *TileClipInfo, resultsChan chan<- *TileClipResult) {
	for tile := range tilesChan {
		startTime := time.Now()

		result := &TileClipResult{
			TileIndex: tile.Index,
			Features:  make([]C.OGRFeatureH, 0),
		}

		// 处理单个瓦片的裁剪更新
		features, err := processSingleTileClipUpdate(inputLayerCopy, updateLayerCopy, tile, workerID, inputTableName, updateTableName, precisionConfig)
		if err != nil {
			result.Error = fmt.Errorf("工作协程 %d 处理瓦片 %d 失败: %v", workerID, tile.Index, err)
		} else {
			result.Features = features
		}

		result.ProcessTime = time.Since(startTime)
		resultsChan <- result
	}
}

// processSingleTileClipUpdate 处理单个瓦片的裁剪更新
func processSingleTileClipUpdate(
	inputLayerCopy, updateLayerCopy *GDALLayer, tile *TileClipInfo, workerID int, inputTableName, updateTableName string, precisionConfig *GeometryPrecisionConfig) (
	[]C.OGRFeatureH, error) {

	// 创建瓦片裁剪后的图层
	inputLayerName := C.CString(fmt.Sprintf("clipped_input_worker%d_tile%d", workerID, tile.Index))
	updateLayerName := C.CString(fmt.Sprintf("clipped_update_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(inputLayerName))
	defer C.free(unsafe.Pointer(updateLayerName))

	// 裁剪两个图层到当前瓦片范围
	inputTableNameC := C.CString(inputTableName)
	updateTableNameC := C.CString(updateTableName)
	defer C.free(unsafe.Pointer(inputTableNameC))
	defer C.free(unsafe.Pointer(updateTableNameC))

	clippedInputLayerPtr := C.clipLayerToTile(inputLayerCopy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), inputLayerName, inputTableNameC)

	clippedUpdateLayerPtr := C.clipLayerToTile(updateLayerCopy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), updateLayerName, updateTableNameC)

	if clippedInputLayerPtr == nil || clippedUpdateLayerPtr == nil {
		if clippedInputLayerPtr != nil {
			C.OGR_L_Dereference(clippedInputLayerPtr)
		}
		if clippedUpdateLayerPtr != nil {
			C.OGR_L_Dereference(clippedUpdateLayerPtr)
		}
		return nil, fmt.Errorf("裁剪图层到瓦片失败")
	}
	defer C.OGR_L_Dereference(clippedInputLayerPtr)
	defer C.OGR_L_Dereference(clippedUpdateLayerPtr)

	clippedInputLayer := &GDALLayer{layer: clippedInputLayerPtr}
	clippedUpdateLayer := &GDALLayer{layer: clippedUpdateLayerPtr}

	// 检查裁剪后的图层是否有要素
	inputCount := clippedInputLayer.GetFeatureCount()
	updateCount := clippedUpdateLayer.GetFeatureCount()

	if inputCount == 0 && updateCount == 0 {
		return []C.OGRFeatureH{}, nil
	}

	// 创建结果临时图层
	resultTempName := C.CString(fmt.Sprintf("update_result_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(resultTempName))

	srs := clippedInputLayer.GetSpatialRef()
	if srs == nil {
		srs = clippedUpdateLayer.GetSpatialRef()
	}

	resultTempPtr := C.createMemoryLayer(resultTempName, C.wkbMultiPolygon, srs)
	if resultTempPtr == nil {
		return nil, fmt.Errorf("创建结果临时图层失败")
	}
	defer C.OGR_L_Dereference(resultTempPtr)

	resultTemp := &GDALLayer{layer: resultTempPtr}

	// 添加字段定义
	err := addUpdateFields(resultTemp, clippedInputLayer, clippedUpdateLayer, inputTableName, updateTableName)
	if err != nil {
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	// 执行更新分析
	err = executeUpdateAnalysis(clippedInputLayer, clippedUpdateLayer, resultTemp, inputTableName, updateTableName, nil)
	if err != nil {
		return nil, fmt.Errorf("瓦片更新分析失败: %v", err)
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

// createUpdateAnalysisResultLayer 创建更新分析结果图层
func createUpdateAnalysisResultLayer(inputLayer, updateLayer *GDALLayer, inputTableName, updateTableName string) (*GDALLayer, error) {
	layerName := C.CString("update_result")
	defer C.free(unsafe.Pointer(layerName))

	// 获取空间参考系统
	srs := inputLayer.GetSpatialRef()
	if srs == nil {
		srs = updateLayer.GetSpatialRef()
	}

	// 创建结果图层
	resultLayerPtr := C.createMemoryLayer(layerName, C.wkbMultiPolygon, srs)
	if resultLayerPtr == nil {
		return nil, fmt.Errorf("创建结果图层失败")
	}

	resultLayer := &GDALLayer{layer: resultLayerPtr}
	runtime.SetFinalizer(resultLayer, (*GDALLayer).cleanup)

	// 添加字段定义
	err := addUpdateFields(resultLayer, inputLayer, updateLayer, inputTableName, updateTableName)
	if err != nil {
		resultLayer.Close()
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	return resultLayer, nil
}

// addUpdateFields 添加更新分析的字段
func addUpdateFields(resultLayer, inputLayer, updateLayer *GDALLayer, inputTableName, updateTableName string) error {
	// 首先添加来源标识字段
	sourceFieldName := C.CString(SourceIdentifierField)
	sourceFieldDefn := C.OGR_Fld_Create(sourceFieldName, C.OFTString)
	C.OGR_Fld_SetWidth(sourceFieldDefn, 50)
	err := C.OGR_L_CreateField(resultLayer.layer, sourceFieldDefn, 1)
	C.OGR_Fld_Destroy(sourceFieldDefn)
	C.free(unsafe.Pointer(sourceFieldName))

	if err != C.OGRERR_NONE {
		return fmt.Errorf("创建来源标识字段失败，错误代码: %d", int(err))
	}

	// 合并两个图层的字段（不使用前缀）
	err1 := addLayerFieldsWithoutPrefix(resultLayer, inputLayer)
	if err1 != nil {
		return fmt.Errorf("添加输入图层字段失败: %v", err1)
	}

	err2 := addLayerFieldsWithoutPrefix(resultLayer, updateLayer)
	if err2 != nil {
		return fmt.Errorf("添加更新图层字段失败: %v", err2)
	}

	return nil
}

// executeUpdateAnalysis 执行更新分析
func executeUpdateAnalysis(inputLayer, updateLayer, resultLayer *GDALLayer, inputTableName, updateTableName string, progressCallback ProgressCallback) error {
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

	// 执行更新操作
	return executeGDALUpdateWithProgress(inputLayer, updateLayer, resultLayer, options, progressCallback)
}

// executeGDALUpdateWithProgress 执行带进度的GDAL更新操作
func executeGDALUpdateWithProgress(inputLayer, updateLayer, resultLayer *GDALLayer, options **C.char, progressCallback ProgressCallback) error {
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

	// 调用GDAL的更新函数
	var err C.OGRErr
	if progressCallback != nil {
		err = C.performUpdateWithProgress(inputLayer.layer, updateLayer.layer, resultLayer.layer, options, progressArg)
	} else {
		err = C.OGR_L_Update(inputLayer.layer, updateLayer.layer, resultLayer.layer, options, nil, nil)
	}

	if err != C.OGRERR_NONE {
		return fmt.Errorf("GDAL更新操作失败，错误代码: %d", int(err))
	}

	return nil
}

// createLayerWithUpdateIdentifierField 创建一个带有更新标识字段的新图层
func createLayerWithUpdateIdentifierField(sourceLayer *GDALLayer, identifierFieldName string) (*GDALLayer, error) {
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

	fmt.Printf("成功创建带更新标识字段的图层，共处理 %d 个要素\n", featureID-1)
	return result, nil
}
