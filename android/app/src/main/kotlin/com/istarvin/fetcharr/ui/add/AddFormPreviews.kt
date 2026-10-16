package com.istarvin.fetcharr.ui.add

import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.istarvin.fetcharr.incoming.CategoryOption
import com.istarvin.fetcharr.incoming.SeasonEpisode
import com.istarvin.fetcharr.ui.incoming.episodeChecklist
import com.istarvin.fetcharr.ui.incoming.ItemForm
import com.istarvin.fetcharr.ui.incoming.ItemFormActions
import com.istarvin.fetcharr.ui.theme.FetcharrTheme

private val previewState = AddUiState(
    settingsLoaded = true,
    serverConfigured = true,
    url = "https://example.com/media/a-long-file-name-for-the-selected-episode.mkv",
    categories = listOf(
        CategoryOption("movies", "Movies", CategoryOption.LAYOUT_MOVIE),
        CategoryOption("tv", "TV shows", CategoryOption.LAYOUT_TV),
    ),
    categoryId = "tv",
    form = ItemForm(
        show = "A show with a long name that stays editable on a small screen",
        season = "2",
        episode = "12",
        episodeTitle = "A long episode title shared from the browser",
        year = "2026",
    ),
    headers = listOf(HeaderRow(1, "Referer", "https://example.com/watch/season/2/episode/12")),
    subtitles = listOf(SubtitleRow(1, "https://example.com/subtitles/episode-12.en.vtt", "English")),
)

@Composable
private fun PreviewAdd(state: AddUiState, dark: Boolean = false) {
    FetcharrTheme(darkTheme = dark, dynamicColor = false) {
        AddContent(
            state = state,
            onBack = {},
            onOpenSettings = {},
            onUrl = {},
            onSelectCategory = {},
            actions = ItemFormActions({}, {}, {}, {}, {}, {}),
            onHeaderName = { _, _ -> },
            onHeaderValue = { _, _ -> },
            onRemoveHeader = {},
            onAddHeader = {},
            onSubtitleUrl = { _, _ -> },
            onSubtitleLanguage = { _, _ -> },
            onRemoveSubtitle = {},
            onAddSubtitle = {},
            onSubmit = {},
        )
    }
}

@Preview(name = "Add populated phone", widthDp = 390, heightDp = 844)
@Composable
private fun AddPopulatedPreview() = PreviewAdd(previewState)

@Preview(name = "Add long input, large text, dark", widthDp = 390, heightDp = 844, fontScale = 1.5f)
@Composable
private fun AddLargeTextPreview() = PreviewAdd(previewState, dark = true)

@Preview(name = "Add expanded form", widthDp = 840, heightDp = 1100)
@Composable
private fun AddExpandedPreview() = PreviewAdd(previewState)

@Preview(name = "Add no server", widthDp = 390, heightDp = 844)
@Composable
private fun AddNoServerPreview() = PreviewAdd(AddUiState(settingsLoaded = true))

@Preview(name = "Incoming episodes, long titles", widthDp = 390, heightDp = 600, fontScale = 1.5f)
@Composable
private fun IncomingEpisodesPreview() {
    FetcharrTheme(dynamicColor = false) {
        Surface {
            LazyColumn(Modifier.padding(24.dp)) {
                episodeChecklist(
                    episodes = listOf(
                        SeasonEpisode(1, "An episode title that wraps across several lines at large text sizes", "https://example.com/1", emptyMap(), emptyList(), true),
                        SeasonEpisode(2, "Another episode", "https://example.com/2", emptyMap(), emptyList(), false),
                    ),
                    onToggle = {},
                    onSetAll = {},
                )
            }
        }
    }
}
