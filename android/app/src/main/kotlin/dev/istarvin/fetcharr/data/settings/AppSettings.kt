package dev.istarvin.fetcharr.data.settings

import dev.istarvin.fetcharr.data.network.dto.CategoryDto

/** Everything persisted in DataStore. */
data class AppSettings(
    /** Normalized base URL, or blank when no server is saved. */
    val serverUrl: String = "",
    /** API token, or blank when none. */
    val token: String = "",
    val defaultMovieCategoryId: String? = null,
    val defaultTvCategoryId: String? = null,
    /** Categories from the last successful `GET /api/info`. */
    val cachedCategories: List<CategoryDto> = emptyList(),
    /** Newest `finished_at` processed, epoch ms. Null until initialised. */
    val lastSeenFinishedAt: Long? = null,
)
