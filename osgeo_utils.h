// osgeo_utils.h
#ifndef OSGEO_UTILS_H
#define OSGEO_UTILS_H

#include <gdal.h>
#include <gdal_alg.h>
#include <ogr_api.h>
#include <ogr_srs_api.h>
#include <cpl_error.h>
#include <cpl_conv.h>
#include <cpl_string.h>
#include <stdlib.h>

#ifdef __cplusplus
extern "C" {
#endif

// 声明外部函数，避免重复定义
extern int handleProgressUpdate(double, char*, void*);

// 函数声明
OGRLayerH createMemoryLayer(const char* layerName, OGRwkbGeometryType geomType, OGRSpatialReferenceH srs);
int addFieldToLayer(OGRLayerH layer, const char* fieldName, OGRFieldType fieldType);
void copyFieldValue(OGRFeatureH srcFeature, OGRFeatureH dstFeature, int srcFieldIndex, int dstFieldIndex);
int progressCallback(double dfComplete, const char *pszMessage, void *pProgressArg);
OGRLayerH cloneLayerToMemory(OGRLayerH sourceLayer, const char* layerName);
int copyFeaturesWithSpatialFilter(OGRLayerH sourceLayer, OGRLayerH targetLayer, OGRGeometryH filterGeom);
int copyAllFeatures(OGRLayerH sourceLayer, OGRLayerH targetLayer);
int isFeatureOnBorder(OGRFeatureH feature, double minX, double minY, double maxX, double maxY, double buffer);
int geometryWKTEqual(OGRGeometryH geom1, OGRGeometryH geom2);
OGRGeometryH setPrecisionIfNeeded(OGRGeometryH geom, double gridSize, int flags);
int setLayerGeometryPrecision(OGRLayerH layer, double gridSize, int flags);
OGRFeatureH setFeatureGeometryPrecision(OGRFeatureH feature, double gridSize, int flags);
OGRGeometryH forceGeometryType(OGRGeometryH geom, OGRwkbGeometryType targetType);
OGRGeometryH mergeGeometryCollection(OGRGeometryH geomCollection, OGRwkbGeometryType targetType);
OGRGeometryH normalizeGeometryType(OGRGeometryH geom, OGRwkbGeometryType expectedType);
OGRGeometryH createTileClipGeometry(double minX, double minY, double maxX, double maxY);
OGRLayerH clipLayerToTile(OGRLayerH sourceLayer, double minX, double minY, double maxX, double maxY, const char* layerName, const char* sourceIdentifier);
#ifdef __cplusplus
}
#endif

#endif // OSGEO_UTILS_H
