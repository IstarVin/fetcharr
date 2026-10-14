package dev.istarvin.fetcharr.ui.incoming

import dev.istarvin.fetcharr.incoming.IncomingSheet
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Base64

/** Intent strings -> router -> hub -> coordinator, on virtual time. */
@OptIn(ExperimentalCoroutinesApi::class)
class IncomingHubFlowTest {
    private val view = IntentRouter.ACTION_VIEW

    private fun TestScope.hub() = IncomingHub(dev.istarvin.fetcharr.incoming.IncomingCoordinator(this), IntentRouter())

    private fun b64(json: String) = Base64.getUrlEncoder().withoutPadding().encodeToString(json.toByteArray())

    private fun episodePayload(n: Int, show: String = "Example Show") = b64(
        """{"category":"tv","headers":{"Referer":"https://stream.example/","X-Ep":"$n"},"single":{"url":"https://cdn.example/s01e$n.mp4",""" +
            """"title":"Ep $n","showName":"$show","season":1,"episode":$n,"year":2026,"subtitles":[{"url":"https://cdn.example/$n.en.vtt","lang":"en"}]}}""",
    )

    @Test
    fun tenRapidEpisodesMergeIntoOneSheetAndDownloadWaitsTwoSecondsAfterTheLast() = runTest {
        val hub = hub()
        for (n in 1..10) {
            hub.handleIntent(view, "fetcharr://download", null, episodePayload(n, if (n % 2 == 0) "EXAMPLE show" else "Example Show"))
            if (n < 10) advanceTimeBy(150)
        }
        runCurrent()
        val state = hub.coordinator.state.value
        val sheet = state.sheet as IncomingSheet.SeasonSheet
        assertEquals(10, sheet.episodes.size)
        assertEquals((1..10).toList(), sheet.episodes.map { it.episode })
        assertEquals(0, state.waitingCount)
        assertEquals("Each episode keeps its own headers", "7", sheet.episodes[6].headers["X-Ep"])

        fun ui(receiving: Boolean) = IncomingUiState(
            sheet = sheet, receiving = receiving, settingsLoaded = true, serverConfigured = true,
            serverCategories = listOf(dev.istarvin.fetcharr.incoming.CategoryOption("tv", "TV", "tv")),
            categoryId = "tv", form = ItemForm(show = sheet.showName, season = "1"),
        )

        // The last episode just arrived; Download stays disabled for 2 s after it.
        assertTrue(hub.coordinator.state.value.receiving)
        assertFalse(ui(hub.coordinator.state.value.receiving).canDownload)
        advanceTimeBy(1_999)
        assertTrue(hub.coordinator.state.value.receiving)
        assertFalse(ui(hub.coordinator.state.value.receiving).canDownload)
        advanceTimeBy(1)
        runCurrent()
        assertFalse(hub.coordinator.state.value.receiving)
        val ready = ui(hub.coordinator.state.value.receiving)
        assertTrue(ready.canDownload)
        assertEquals(10, ready.selectedCount)
        assertTrue(ready.isSeasonMode)
    }

    @Test
    fun unreadablePayloadEmitsSnackbarAndOpensNothing() = runTest {
        val hub = hub()
        hub.handleIntent(view, "fetcharr://download", null, "%%%")
        assertEquals(HubEvent.Snackbar("Couldn't read the download request"), hub.events.first())
        assertEquals(null, hub.coordinator.state.value.sheet)
    }

    @Test
    fun shareIntentRequestsAddScreen() = runTest {
        val hub = hub()
        hub.handleIntent(IntentRouter.ACTION_SEND, null, "look https://example.com/v.mp4 now", null)
        hub.handleIntent(IntentRouter.ACTION_SEND, null, null, null)
        assertEquals(HubEvent.OpenAdd("https://example.com/v.mp4"), hub.events.first())
        hub.handleIntent("android.intent.action.MAIN", null, null, null)
        assertEquals(HubEvent.OpenAdd(null), hub.events.first())
    }
}
