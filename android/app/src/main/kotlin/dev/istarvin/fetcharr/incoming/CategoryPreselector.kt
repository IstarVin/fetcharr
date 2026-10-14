package dev.istarvin.fetcharr.incoming

/** A server category as far as preselection cares. [layout] is "movie", "tv" or "flat". */
data class CategoryOption(val id: String, val label: String, val layout: String) {
    companion object {
        const val LAYOUT_MOVIE = "movie"
        const val LAYOUT_TV = "tv"
        const val LAYOUT_FLAT = "flat"
    }
}

/**
 * Category preselection (spec 4.2).
 *
 * "Layout fits": an episodic request fits `tv` categories only; any other request fits `movie` and `flat`.
 * The hint rule (1) matches an id regardless of layout, as the spec states; defaults (2, 3) are skipped
 * when absent from the server list or not fitting, and selection falls through to rule 4.
 */
object CategoryPreselector {

    fun preselect(
        request: IncomingRequest,
        serverCategories: List<CategoryOption>,
        defaultMovieCategoryId: String?,
        defaultTvCategoryId: String?,
    ): String? {
        // Rule 2 reads "has a season and episode (or is a batch)", so showName is not required here.
        val episodic = when (request) {
            is IncomingRequest.Batch -> true
            is IncomingRequest.Single -> request.season != null && request.episode != null
        }

        // 1. The hint matches a server category id (exact match first, then case-insensitive).
        request.categoryHint?.takeIf { it.isNotBlank() }?.let { hint ->
            val match = serverCategories.firstOrNull { it.id == hint }
                ?: serverCategories.firstOrNull { it.id.equals(hint, ignoreCase = true) }
            if (match != null) return match.id
        }

        // 2./3. The default for the request kind, if it exists on the server and fits.
        val defaultId = if (episodic) defaultTvCategoryId else defaultMovieCategoryId
        if (defaultId != null) {
            serverCategories.firstOrNull { it.id == defaultId && fits(it, episodic) }?.let { return it.id }
        }

        // 4. First category whose layout fits.
        return serverCategories.firstOrNull { fits(it, episodic) }?.id
    }

    fun fits(category: CategoryOption, episodic: Boolean): Boolean =
        if (episodic) {
            category.layout == CategoryOption.LAYOUT_TV
        } else {
            category.layout == CategoryOption.LAYOUT_MOVIE || category.layout == CategoryOption.LAYOUT_FLAT
        }
}
