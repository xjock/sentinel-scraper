cmake_minimum_required(VERSION 3.20)
project(sentinel2-go VERSION 0.1.1 LANGUAGES NONE)

# -----------------------------------------------------------------------------
# 1. Find Go
# -----------------------------------------------------------------------------
find_program(GO_EXECUTABLE go REQUIRED)
if(NOT GO_EXECUTABLE)
    message(FATAL_ERROR "Go not found. Please install Go: https://go.dev/dl/")
endif()

execute_process(
    COMMAND ${GO_EXECUTABLE} version
    OUTPUT_VARIABLE GO_VERSION
    OUTPUT_STRIP_TRAILING_WHITESPACE
)
message(STATUS "Found Go: ${GO_VERSION}")

# -----------------------------------------------------------------------------
# 2. Build the Go binary
# -----------------------------------------------------------------------------
set(GO_OUTPUT ${CMAKE_BINARY_DIR}/sentinel2-scraper${CMAKE_EXECUTABLE_SUFFIX})

# Collect Go sources for dependency tracking
file(GLOB GO_SOURCES ${CMAKE_SOURCE_DIR}/*.go)

add_custom_command(
    OUTPUT ${GO_OUTPUT}
    COMMAND ${GO_EXECUTABLE} build -o ${GO_OUTPUT} .
    WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}
    DEPENDS ${GO_SOURCES}
    COMMENT "Building sentinel2-scraper..."
    VERBATIM
)

add_custom_target(build-go ALL DEPENDS ${GO_OUTPUT})

# -----------------------------------------------------------------------------
# 3. GDAL toolchain bundling (optional)
# -----------------------------------------------------------------------------
set(GDAL_TOOLCHAIN_DIR "" CACHE PATH
    "Path to GDAL toolchain for runtime bundling.\n"
    "Example: D:/Dev/toolchain/gdal353_msvc142_x64_v3")

if(GDAL_TOOLCHAIN_DIR)
    if(NOT IS_DIRECTORY "${GDAL_TOOLCHAIN_DIR}")
        message(FATAL_ERROR "GDAL_TOOLCHAIN_DIR does not exist: ${GDAL_TOOLCHAIN_DIR}")
    endif()

    set(GDAL_BIN_DIR ${GDAL_TOOLCHAIN_DIR}/bin)
    message(STATUS "GDAL toolchain: ${GDAL_TOOLCHAIN_DIR}")

    # Required GDAL executables
    set(GDAL_EXES
        gdalbuildvrt
        gdal_translate
        gdalwarp
        gdal_trace_outline
        pkRenew
    )

    foreach(exe ${GDAL_EXES})
        set(exe_path ${GDAL_BIN_DIR}/${exe}${CMAKE_EXECUTABLE_SUFFIX})
        if(EXISTS ${exe_path})
            list(APPEND GDAL_BUNDLE_FILES ${exe_path})
        else()
            message(WARNING "GDAL executable not found: ${exe_path}")
        endif()
    endforeach()

    # Windows DLLs
    if(WIN32)
        file(GLOB GDAL_DLLS ${GDAL_BIN_DIR}/*.dll)
        list(APPEND GDAL_BUNDLE_FILES ${GDAL_DLLS})
    endif()

    # PROJ data
    if(EXISTS "${GDAL_TOOLCHAIN_DIR}/share/proj")
        set(GDAL_PROJ_DATA "${GDAL_TOOLCHAIN_DIR}/share/proj")
    elseif(EXISTS "${GDAL_BIN_DIR}/../share/proj")
        get_filename_component(GDAL_PROJ_DATA "${GDAL_BIN_DIR}/../share/proj" ABSOLUTE)
    endif()

    if(GDAL_PROJ_DATA)
        message(STATUS "PROJ data: ${GDAL_PROJ_DATA}")
    endif()
endif()

# -----------------------------------------------------------------------------
# 4. Installation
# -----------------------------------------------------------------------------
install(PROGRAMS ${GO_OUTPUT} DESTINATION .)
install(FILES ${CMAKE_SOURCE_DIR}/config.json DESTINATION .)

if(GDAL_BUNDLE_FILES)
    foreach(f ${GDAL_BUNDLE_FILES})
        install(FILES ${f} DESTINATION .)
    endforeach()
endif()

if(GDAL_PROJ_DATA)
    install(DIRECTORY ${GDAL_PROJ_DATA} DESTINATION share)
endif()

# -----------------------------------------------------------------------------
# 5. CPack packaging
# -----------------------------------------------------------------------------
set(CPACK_PACKAGE_NAME "sentinel2-scraper")
set(CPACK_PACKAGE_VERSION ${PROJECT_VERSION})
set(CPACK_PACKAGE_VENDOR "sentinel2-scraper")
set(CPACK_PACKAGE_DESCRIPTION_SUMMARY "Sentinel-2 L2A imagery downloader and RGB composer")
set(CPACK_GENERATOR "ZIP")

if(WIN32)
    list(APPEND CPACK_GENERATOR "NSIS")
    set(CPACK_NSIS_EXECUTABLES_DIRECTORY ".")
    set(CPACK_NSIS_ENABLE_UNINSTALL_BEFORE_INSTALL ON)
endif()

include(CPack)
