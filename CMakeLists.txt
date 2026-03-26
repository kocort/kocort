cmake_minimum_required(VERSION 3.21)

project(KocortBackends C CXX)

include(CheckLanguage)
include(GNUInstallDirs)

find_package(Threads REQUIRED)

set(CMAKE_BUILD_TYPE Release)
set(BUILD_SHARED_LIBS ON)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS ON)

# Output directories (required by ggml backend build)
set(CMAKE_RUNTIME_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR}/bin)
set(CMAKE_LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR}/lib)

# On Windows with MinGW, remove the "lib" prefix from DLL names
# so that the ggml loader (which searches for "ggml-*.dll") can find them,
# and so that internal cross-references also use the correct names.
if(WIN32)
    set(CMAKE_SHARED_MODULE_PREFIX "")
    set(CMAKE_SHARED_LIBRARY_PREFIX "")
endif()

# ggml configuration — build GPU backends as dynamically-loadable modules
set(GGML_BUILD ON)
set(GGML_SHARED ON)
set(GGML_BACKEND_DL ON)
set(GGML_BACKEND_SHARED ON)
set(GGML_SCHED_MAX_COPIES 4)
set(GGML_LLAMAFILE OFF)
set(GGML_VERSION "0.0.0" CACHE STRING "ggml version" FORCE)
set(GGML_VERSION_MAJOR 0 CACHE STRING "ggml version major" FORCE)

# C preprocessor definitions needed by ggml.c
add_compile_definitions(GGML_VERSION="0.0.0")
add_compile_definitions(GGML_COMMIT="kocort")

# Disable backends we don't need to keep build fast
set(GGML_BLAS OFF CACHE BOOL "" FORCE)
set(GGML_CANN OFF CACHE BOOL "" FORCE)
set(GGML_METAL OFF CACHE BOOL "" FORCE)
set(GGML_MUSA OFF CACHE BOOL "" FORCE)
set(GGML_RPC OFF CACHE BOOL "" FORCE)
set(GGML_SYCL OFF CACHE BOOL "" FORCE)
set(GGML_WebGPU OFF CACHE BOOL "" FORCE)
set(GGML_OpenCL OFF CACHE BOOL "" FORCE)
set(GGML_Hexagon OFF CACHE BOOL "" FORCE)
set(GGML_ZenDNN OFF CACHE BOOL "" FORCE)
set(GGML_zDNN OFF CACHE BOOL "" FORCE)

# CPU: build a single generic variant (no need for all CPU variants)
set(GGML_CPU_ALL_VARIANTS OFF CACHE BOOL "" FORCE)

# Install directory for backend libraries (lib/kocort/)
if(NOT DEFINED KOCORT_INSTALL_DIR)
    set(KOCORT_INSTALL_DIR "lib/kocort")
endif()

# ── GPU backend selection ──────────────────────────────────────────
# Vulkan (works with any GPU that has Vulkan drivers — NVIDIA, AMD, Intel)
option(GGML_VULKAN "Enable Vulkan GPU backend" OFF)

# CUDA (requires CUDA Toolkit)
option(GGML_CUDA "Enable CUDA GPU backend" OFF)
if(GGML_CUDA)
    check_language(CUDA)
    if(CMAKE_CUDA_COMPILER)
        if(CMAKE_VERSION VERSION_GREATER_EQUAL "3.24" AND NOT CMAKE_CUDA_ARCHITECTURES)
            set(CMAKE_CUDA_ARCHITECTURES "native")
        endif()
        set(GGML_CUDA_PEER_MAX_BATCH_SIZE 128)
        set(GGML_CUDA_GRAPHS ON)
        set(GGML_CUDA_FA ON)
    else()
        message(WARNING "CUDA compiler not found, disabling CUDA backend")
        set(GGML_CUDA OFF)
    endif()
endif()

# HIP / ROCm (requires AMD ROCm toolkit)
option(GGML_HIP "Enable HIP/ROCm GPU backend" OFF)
if(GGML_HIP)
    # ROCm path detection (from environment or standard locations)
    if(NOT DEFINED ROCM_PATH)
        if(DEFINED ENV{ROCM_PATH})
            set(ROCM_PATH $ENV{ROCM_PATH})
        elseif(EXISTS "/opt/rocm")
            set(ROCM_PATH "/opt/rocm")
        endif()
    endif()
    if(ROCM_PATH)
        list(APPEND CMAKE_PREFIX_PATH ${ROCM_PATH})
    endif()
endif()

# ── Build ggml core + backends ─────────────────────────────────────
add_subdirectory(internal/llama/ggml/src)

# ── Install ────────────────────────────────────────────────────────
install(TARGETS ggml-base ggml
    RUNTIME DESTINATION ${KOCORT_INSTALL_DIR}
    LIBRARY DESTINATION ${KOCORT_INSTALL_DIR}
)
if(TARGET ggml-vulkan)
    install(TARGETS ggml-vulkan
        RUNTIME DESTINATION ${KOCORT_INSTALL_DIR}
        LIBRARY DESTINATION ${KOCORT_INSTALL_DIR}
    )
endif()
if(TARGET ggml-cuda)
    install(TARGETS ggml-cuda
        RUNTIME DESTINATION ${KOCORT_INSTALL_DIR}
        LIBRARY DESTINATION ${KOCORT_INSTALL_DIR}
    )
endif()
if(TARGET ggml-hip)
    install(TARGETS ggml-hip
        RUNTIME DESTINATION ${KOCORT_INSTALL_DIR}
        LIBRARY DESTINATION ${KOCORT_INSTALL_DIR}
    )
endif()
