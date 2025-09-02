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
static OGRErr performEraseWithProgress(OGRLayerH inputLayer,
                                     OGRLayerH methodLayer,
                                     OGRLayerH resultLayer,
                                     char **options,
                                     void *progressData) {
    return OGR_L_Erase(inputLayer, methodLayer, resultLayer, options,
                       progressCallback, progressData);
}

// 修改 clipLayerToTile 函数用于Erase操作，只需要输入图层的标识
static OGRLayerH clipInputLayerToTile(OGRLayerH sourceLayer, double minX, double minY, double maxX, double maxY, const char* layerName) {
    // 创建裁剪几何体
    OGRGeometryH clipGeom = createTileClipGeometry(minX, minY, maxX, maxY);
    if (!clipGeom) return NULL;

    // 获取源图层的空间参考和几何类型
    OGRSpatialReferenceH srs = OGR_L_GetSpatialRef(sourceLayer);
    OGRwkbGeometryType geomType = OGR_L_GetGeomType(sourceLayer);

    // 创建内存图层
    OGRLayerH clippedLayer = createMemoryLayer(layerName, geomType, srs);
    if (!clippedLayer) {
        OGR_G_DestroyGeometry(clipGeom);
        return NULL;
    }

    // 复制原有字段定义
    OGRFeatureDefnH sourceDefn = OGR_L_GetLayerDefn(sourceLayer);
    OGRFeatureDefnH targetDefn = OGR_L_GetLayerDefn(clippedLayer);

    int fieldCount = OGR_FD_GetFieldCount(sourceDefn);
    for (int i = 0; i < fieldCount; i++) {
        OGRFieldDefnH fieldDefn = OGR_FD_GetFieldDefn(sourceDefn, i);
        OGR_L_CreateField(clippedLayer, fieldDefn, TRUE);
    }

    // 设置空间过滤器
    OGR_L_SetSpatialFilter(sourceLayer, clipGeom);
    OGR_L_ResetReading(sourceLayer);

    // 遍历源图层要素，进行裁剪
    OGRFeatureH feature;
    while ((feature = OGR_L_GetNextFeature(sourceLayer)) != NULL) {
        OGRGeometryH geom = OGR_F_GetGeometryRef(feature);
        if (!geom) {
            OGR_F_Destroy(feature);
            continue;
        }

        // 执行几何体裁剪
        OGRGeometryH clippedGeom = OGR_G_Intersection(geom, clipGeom);
        if (clippedGeom && !OGR_G_IsEmpty(clippedGeom)) {
            // 创建新要素
            OGRFeatureH newFeature = OGR_F_Create(targetDefn);

            // 复制所有属性字段
            for (int i = 0; i < fieldCount; i++) {
                if (OGR_F_IsFieldSet(feature, i)) {
                    OGRFieldType fieldType = OGR_Fld_GetType(OGR_FD_GetFieldDefn(sourceDefn, i));
                    switch (fieldType) {
                        case OFTInteger:
                            OGR_F_SetFieldInteger(newFeature, i, OGR_F_GetFieldAsInteger(feature, i));
                            break;
                        case OFTInteger64:
                            OGR_F_SetFieldInteger64(newFeature, i, OGR_F_GetFieldAsInteger64(feature, i));
                            break;
                        case OFTReal:
                            OGR_F_SetFieldDouble(newFeature, i, OGR_F_GetFieldAsDouble(feature, i));
                            break;
                        case OFTString:
                            OGR_F_SetFieldString(newFeature, i, OGR_F_GetFieldAsString(feature, i));
                            break;
                        case OFTDate:
                        case OFTTime:
                        case OFTDateTime: {
                            int year, month, day, hour, minute, second, tzflag;
                            OGR_F_GetFieldAsDateTime(feature, i, &year, &month, &day, &hour, &minute, &second, &tzflag);
                            OGR_F_SetFieldDateTime(newFeature, i, year, month, day, hour, minute, second, tzflag);
                            break;
                        }
                        default:
                            OGR_F_SetFieldString(newFeature, i, OGR_F_GetFieldAsString(feature, i));
                            break;
                    }
                }
            }

            // 设置裁剪后的几何体
            OGR_F_SetGeometry(newFeature, clippedGeom);

            // 添加到结果图层
            OGR_L_CreateFeature(clippedLayer, newFeature);
            OGR_F_Destroy(newFeature);
        }

        if (clippedGeom) {
            OGR_G_DestroyGeometry(clippedGeom);
        }
        OGR_F_Destroy(feature);
    }

    // 清理
    OGR_L_SetSpatialFilter(sourceLayer, NULL);
    OGR_G_DestroyGeometry(clipGeom);

    return clippedLayer;
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

// SpatialEraseAnalysis执行并行空间擦除分析
func SpatialEraseAnalysis(inputLayer, eraseLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error) {
	// 读取输入图层

	defer inputLayer.Close()

	// 读取擦除图层

	defer eraseLayer.Close()

	// 为输入图层添加唯一标识字段（用于后续融合）
	err := addUniqueIdentifierFieldForErase(inputLayer, inputLayer.GetLayerName())
	if err != nil {
		return nil, fmt.Errorf("添加唯一标识字段失败: %v", err)
	}
	inputTable := inputLayer.GetLayerName()
	eraseTable := eraseLayer.GetLayerName()
	// 执行基于瓦片裁剪的并行擦除分析
	resultLayer, err := performTileClipEraseAnalysis(inputLayer, eraseLayer, inputTable, eraseTable, config)
	if err != nil {
		return nil, fmt.Errorf("执行瓦片裁剪擦除分析失败: %v", err)
	}

	// 计算结果数量
	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪并行分块数: %dx%d\n", config.TileCount, config.TileCount)
	fmt.Printf("擦除分析完成，共生成 %d 个要素\n", resultCount)

	// 执行按标识字段的融合操作
	if config.IsMergeTile {
		unionResult, err := performUnionByIdentifierFieldsForErase(resultLayer, inputTable, config.PrecisionConfig, config.ProgressCallback)
		if err != nil {
			return nil, fmt.Errorf("执行融合操作失败: %v", err)
		}

		fmt.Printf("融合操作完成，最终生成 %d 个要素\n", unionResult.ResultCount)
		// 删除临时的_eraseID字段
		err = removeTempIdentifierFieldForErase(unionResult.OutputLayer, inputTable)
		if err != nil {
			fmt.Printf("警告: 删除临时标识字段失败: %v\n", err)
		} else {
			fmt.Printf("成功删除临时标识字段: %s_eraseID\n", inputTable)
		}

		return unionResult, nil
	} else {

		return &GeosAnalysisResult{
			OutputLayer: resultLayer,
			ResultCount: resultCount,
		}, nil
	}

}

// addUniqueIdentifierFieldForErase 为输入图层添加唯一标识字段
func addUniqueIdentifierFieldForErase(inputLayer *GDALLayer, inputTableName string) error {
	// 为输入图层创建带标识字段的副本
	newLayer, err := createLayerWithIdentifierFieldForErase(inputLayer, inputTableName+"_eraseID")
	if err != nil {
		return fmt.Errorf("为图层 %s 创建带标识字段的副本失败: %v", inputTableName, err)
	}

	// 替换原始图层
	inputLayer.Close()
	*inputLayer = *newLayer

	fmt.Printf("成功添加标识字段: %s_eraseID\n", inputTableName)
	return nil
}

// createLayerWithIdentifierFieldForErase 创建一个带有标识字段的新图层（用于擦除操作）
func createLayerWithIdentifierFieldForErase(sourceLayer *GDALLayer, identifierFieldName string) (*GDALLayer, error) {
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

// removeTempIdentifierFieldForErase 删除临时的_eraseID字段
func removeTempIdentifierFieldForErase(resultLayer *GDALLayer, inputTableName string) error {
	fieldName := inputTableName + "_eraseID"
	return deleteFieldFromLayer(resultLayer, fieldName)
}

// performUnionByIdentifierFieldsForErase 执行按标识字段的融合操作（用于擦除）
func performUnionByIdentifierFieldsForErase(inputLayer *GDALLayer, inputTableName string,
	precisionConfig *GeometryPrecisionConfig, progressCallback ProgressCallback) (*GeosAnalysisResult, error) {

	// 构建分组字段列表（只需要输入图层的标识字段）
	groupFields := []string{
		inputTableName + "_eraseID", // 输入表的标识字段
	}

	// 构建输出图层名称
	outputLayerName := fmt.Sprintf("erase_union_%s", inputTableName)
	precisionConfig.GridSize = 0
	// 执行融合操作
	return UnionByFieldsWithPrecision(inputLayer, groupFields, outputLayerName, precisionConfig, progressCallback)
}

// performTileClipEraseAnalysis 执行基于瓦片裁剪的并行擦除分析
func performTileClipEraseAnalysis(inputLayer, eraseLayer *GDALLayer, inputTableName, eraseTableName string, config *ParallelGeosConfig) (*GDALLayer, error) {
	// 如果启用了精度设置，在分块裁剪前对原始图层进行精度处理
	if config.PrecisionConfig != nil && config.PrecisionConfig.Enabled && config.PrecisionConfig.GridSize > 0 {
		flags := config.PrecisionConfig.getFlags()
		gridSize := C.double(config.PrecisionConfig.GridSize)

		C.setLayerGeometryPrecision(inputLayer.layer, gridSize, flags)

		C.setLayerGeometryPrecision(eraseLayer.layer, gridSize, flags)
	}

	// 获取数据范围
	extent, err := getLayersExtent(inputLayer, eraseLayer)
	if err != nil {
		return nil, fmt.Errorf("获取数据范围失败: %v", err)
	}

	// 创建瓦片裁剪信息
	tiles := createTileClipInfos(extent, config.TileCount)
	fmt.Printf("创建了 %d 个瓦片进行裁剪并行处理\n", len(tiles))

	// 创建结果图层
	resultLayer, err := createEraseAnalysisResultLayer(inputLayer, inputTableName)
	if err != nil {
		return nil, fmt.Errorf("创建结果图层失败: %v", err)
	}

	// 为每个工作线程创建图层副本
	inputLayerCopies, eraseLayerCopies, err := createLayerCopies(inputLayer, eraseLayer, config.MaxWorkers)
	if err != nil {
		return nil, fmt.Errorf("创建图层副本失败: %v", err)
	}
	defer cleanupLayerCopies(inputLayerCopies, eraseLayerCopies)

	// 并行处理瓦片裁剪（不再传递精度配置，因为已经在分块前处理了）
	err = processTileClipEraseInParallel(inputLayerCopies, eraseLayerCopies, resultLayer, tiles, inputTableName, eraseTableName, config)
	if err != nil {
		return nil, fmt.Errorf("并行处理瓦片裁剪失败: %v", err)
	}

	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪擦除分析完成，共生成 %d 个要素\n", resultCount)

	return resultLayer, nil
}

// createEraseAnalysisResultLayer 创建擦除分析结果图层
func createEraseAnalysisResultLayer(inputLayer *GDALLayer, inputTableName string) (*GDALLayer, error) {
	layerName := C.CString("erase_result")
	defer C.free(unsafe.Pointer(layerName))

	// 获取空间参考系统
	srs := inputLayer.GetSpatialRef()

	// 创建结果图层
	resultLayerPtr := C.createMemoryLayer(layerName, C.wkbMultiPolygon, srs)
	if resultLayerPtr == nil {
		return nil, fmt.Errorf("创建结果图层失败")
	}

	resultLayer := &GDALLayer{layer: resultLayerPtr}
	runtime.SetFinalizer(resultLayer, (*GDALLayer).cleanup)

	// 添加字段定义 - 只需要输入图层的字段
	err := addEraseFields(resultLayer, inputLayer)
	if err != nil {
		resultLayer.Close()
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	return resultLayer, nil
}

// addEraseFields 添加擦除分析的字段（只复制输入图层的字段）
func addEraseFields(resultLayer, inputLayer *GDALLayer) error {
	// 复制输入图层的字段
	return addLayerFieldsWithoutPrefix(resultLayer, inputLayer)
}

// processTileClipEraseInParallel 并行处理瓦片裁剪擦除
func processTileClipEraseInParallel(inputLayerCopies, eraseLayerCopies []*GDALLayer, resultLayer *GDALLayer, tiles []*TileClipInfo, inputTableName, eraseTableName string, config *ParallelGeosConfig) error {
	// 创建工作池
	tilesChan := make(chan *TileClipInfo, len(tiles))
	resultsChan := make(chan *TileClipResult, len(tiles))

	// 启动工作协程（不再传递精度配置）
	var wg sync.WaitGroup
	for i := 0; i < config.MaxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			processTileClipEraseWorker(workerID, inputLayerCopies[workerID], eraseLayerCopies[workerID], inputTableName, eraseTableName, tilesChan, resultsChan)
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

// processTileClipEraseWorker 瓦片裁剪擦除工作协程
func processTileClipEraseWorker(workerID int, inputLayerCopy, eraseLayerCopy *GDALLayer, inputTableName, eraseTableName string, tilesChan <-chan *TileClipInfo, resultsChan chan<- *TileClipResult) {
	for tile := range tilesChan {
		startTime := time.Now()

		result := &TileClipResult{
			TileIndex: tile.Index,
			Features:  make([]C.OGRFeatureH, 0),
		}

		// 处理单个瓦片的裁剪擦除（不再传递精度配置）
		features, err := processSingleTileClipErase(inputLayerCopy, eraseLayerCopy, tile, workerID, inputTableName, eraseTableName)
		if err != nil {
			result.Error = fmt.Errorf("工作协程 %d 处理瓦片 %d 失败: %v", workerID, tile.Index, err)
		} else {
			result.Features = features
		}

		result.ProcessTime = time.Since(startTime)
		resultsChan <- result
	}
}

// processSingleTileClipErase 处理单个瓦片的裁剪擦除
// processSingleTileClipErase 处理单个瓦片的裁剪擦除
func processSingleTileClipErase(
	inputLayerCopy, eraseLayerCopy *GDALLayer, tile *TileClipInfo, workerID int, inputTableName, eraseTableName string) (
	[]C.OGRFeatureH, error) {

	// 创建瓦片裁剪后的图层
	inputLayerName := C.CString(fmt.Sprintf("clipped_input_worker%d_tile%d", workerID, tile.Index))
	eraseLayerName := C.CString(fmt.Sprintf("clipped_erase_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(inputLayerName))
	defer C.free(unsafe.Pointer(eraseLayerName))

	// 裁剪输入图层到当前瓦片范围
	clippedInputLayerPtr := C.clipInputLayerToTile(inputLayerCopy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), inputLayerName)

	// 裁剪擦除图层到当前瓦片范围
	clippedEraseLayerPtr := C.clipInputLayerToTile(eraseLayerCopy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), eraseLayerName)

	if clippedInputLayerPtr == nil {
		if clippedEraseLayerPtr != nil {
			C.OGR_L_Dereference(clippedEraseLayerPtr)
		}
		return nil, fmt.Errorf("裁剪输入图层到瓦片失败")
	}
	defer C.OGR_L_Dereference(clippedInputLayerPtr)

	if clippedEraseLayerPtr != nil {
		defer C.OGR_L_Dereference(clippedEraseLayerPtr)
	}

	clippedInputLayer := &GDALLayer{layer: clippedInputLayerPtr}
	var clippedEraseLayer *GDALLayer
	if clippedEraseLayerPtr != nil {
		clippedEraseLayer = &GDALLayer{layer: clippedEraseLayerPtr}
	}

	// 检查裁剪后的图层是否有要素
	inputCount := clippedInputLayer.GetFeatureCount()
	if inputCount == 0 {
		return []C.OGRFeatureH{}, nil
	}

	var eraseCount int = 0
	if clippedEraseLayer != nil {
		eraseCount = clippedEraseLayer.GetFeatureCount()
	}

	// 如果没有擦除要素，直接返回输入要素
	if eraseCount == 0 {
		features := make([]C.OGRFeatureH, 0)
		clippedInputLayer.ResetReading()
		clippedInputLayer.IterateFeatures(func(feature C.OGRFeatureH) {
			clonedFeature := C.OGR_F_Clone(feature)
			if clonedFeature != nil {
				features = append(features, clonedFeature)
			}
		})
		return features, nil
	}

	// 移除了分块后的精度处理代码

	// 创建结果临时图层
	resultTempName := C.CString(fmt.Sprintf("erase_result_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(resultTempName))

	srs := clippedInputLayer.GetSpatialRef()
	resultTempPtr := C.createMemoryLayer(resultTempName, C.wkbMultiPolygon, srs)
	if resultTempPtr == nil {
		return nil, fmt.Errorf("创建结果临时图层失败")
	}
	defer C.OGR_L_Dereference(resultTempPtr)

	resultTemp := &GDALLayer{layer: resultTempPtr}

	// 添加字段定义
	err := addEraseFields(resultTemp, clippedInputLayer)
	if err != nil {
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	// 执行擦除分析
	err = executeEraseAnalysis(clippedInputLayer, clippedEraseLayer, resultTemp, nil)
	if err != nil {
		return nil, fmt.Errorf("瓦片擦除分析失败: %v", err)
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

// executeEraseAnalysis 执行擦除分析
func executeEraseAnalysis(inputLayer, eraseLayer, resultLayer *GDALLayer, progressCallback ProgressCallback) error {
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

	// 执行擦除操作
	return executeGDALEraseWithProgress(inputLayer, eraseLayer, resultLayer, options, progressCallback)
}

// executeGDALEraseWithProgress 执行带进度的GDAL擦除操作
func executeGDALEraseWithProgress(inputLayer, eraseLayer, resultLayer *GDALLayer, options **C.char, progressCallback ProgressCallback) error {
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

	// 调用GDAL的擦除函数
	var err C.OGRErr
	if progressCallback != nil {
		err = C.performEraseWithProgress(inputLayer.layer, eraseLayer.layer, resultLayer.layer, options, progressArg)
	} else {
		err = C.OGR_L_Erase(inputLayer.layer, eraseLayer.layer, resultLayer.layer, options, nil, nil)
	}

	if err != C.OGRERR_NONE {
		return fmt.Errorf("GDAL擦除操作失败，错误代码: %d", int(err))
	}

	return nil
}
