package com.istarvin.fetcharr.data.sync

import app.cash.turbine.test
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class SyncManagerTest {
    private class FakeConnector : WebSocketConnector {
        class Attempt(val config: ServerConfig, val listener: WebSocketConnector.Listener, val time: Long) :
            WebSocketConnector.Handle {
            var closed = false
            override fun close() {
                closed = true
            }

            fun open() = listener.onOpen()
            fun send(text: String) = listener.onMessage(text)
            fun fail() = listener.onFailure(java.io.IOException("boom"))
            fun serverClose() = listener.onClosed()
        }

        var clock: () -> Long = { 0L }
        val attempts = mutableListOf<Attempt>()
        override fun connect(config: ServerConfig, listener: WebSocketConnector.Listener): WebSocketConnector.Handle =
            Attempt(config, listener, clock()).also { attempts += it }
    }

    private class FakeConfig(initial: ServerConfig?) : ServerConfigProvider {
        override val config = MutableStateFlow(initial)
    }

    private val a = ServerConfig("http://a:8080", "tok")
    private val b = ServerConfig("http://b:8080", "tok")

    private class Env(scope: TestScope, config: ServerConfig?) {
        val connector = FakeConnector().also { it.clock = { scope.currentTime } }
        val config = FakeConfig(config)
        val store = ActiveJobsStore()
        var refreshes = 0
        val manager = SyncManager(
            connector, this.config, store, { refreshes++ }, FetcharrJson, scope.backgroundScope,
        )
    }

    private fun TestScope.env(config: ServerConfig? = a) = Env(this, config)

    @Test
    fun idleUntilAcquired() = runTest {
        val env = env()
        runCurrent()
        assertTrue(env.connector.attempts.isEmpty())
        assertEquals(ConnectionState.Disconnected(null), env.manager.state.value)
    }

    @Test
    fun firstConnectIsImmediateWithTheSavedConfig() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        assertEquals(1, env.connector.attempts.size)
        assertEquals(0L, env.connector.attempts[0].time)
        assertEquals(a, env.connector.attempts[0].config)
        assertEquals(ConnectionState.Connecting, env.manager.state.value)
    }

    @Test
    fun noConfigMeansNotConfiguredAndNoSocket() = runTest {
        val env = env(null)
        env.manager.acquire("ui")
        runCurrent()
        assertTrue(env.connector.attempts.isEmpty())
        assertEquals(ConnectionState.NotConfigured, env.manager.state.value)
        env.config.config.value = a
        runCurrent()
        assertEquals(1, env.connector.attempts.size)
    }

    @Test
    fun failuresRetryAtDoublingDelaysCappedAtThirtySeconds() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        val expectedDelays = listOf(1L, 2L, 4L, 8L, 16L, 30L, 30L, 30L).map { it * 1000 }
        var expectedTime = 0L
        for ((i, d) in expectedDelays.withIndex()) {
            env.connector.attempts.last().fail()
            runCurrent()
            assertEquals(ConnectionState.Disconnected((d / 1000).toInt()), env.manager.state.value)
            advanceTimeBy(d - 1)
            runCurrent()
            assertEquals(i + 1, env.connector.attempts.size)
            advanceTimeBy(1)
            runCurrent()
            expectedTime += d
            assertEquals(i + 2, env.connector.attempts.size)
            assertEquals(expectedTime, env.connector.attempts.last().time)
        }
    }

    @Test
    fun dropAfterSuccessfulOpenRetriesAfterOneSecond() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        repeat(3) {
            env.connector.attempts.last().fail()
            runCurrent()
            advanceTimeBy(30_000)
            runCurrent()
        }
        val sizeBefore = env.connector.attempts.size
        env.connector.attempts.last().open()
        runCurrent()
        assertEquals(ConnectionState.Connected, env.manager.state.value)
        val openedAt = currentTime
        env.connector.attempts.last().serverClose()
        runCurrent()
        assertEquals(ConnectionState.Disconnected(1), env.manager.state.value)
        advanceTimeBy(999)
        runCurrent()
        assertEquals(sizeBefore, env.connector.attempts.size)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(sizeBefore + 1, env.connector.attempts.size)
        assertEquals(openedAt + 1_000, env.connector.attempts.last().time)
    }

    @Test
    fun stateTransitionsAndCountdown() = runTest {
        val env = env()
        env.manager.state.test {
            assertEquals(ConnectionState.Disconnected(null), awaitItem())
            env.manager.acquire("ui")
            assertEquals(ConnectionState.Connecting, awaitItem())
            env.connector.attempts.last().open()
            assertEquals(ConnectionState.Connected, awaitItem())
            env.connector.attempts.last().fail()
            assertEquals(ConnectionState.Disconnected(1), awaitItem())
            assertEquals(ConnectionState.Connecting, awaitItem())
            env.connector.attempts.last().fail()
            assertEquals(ConnectionState.Disconnected(2), awaitItem())
            assertEquals(ConnectionState.Disconnected(1), awaitItem())
            assertEquals(ConnectionState.Connecting, awaitItem())
            cancelAndIgnoreRemainingEvents()
        }
    }

    @Test
    fun onOpenRefreshesInfoAndEmitsConnected() = runTest {
        val env = env()
        env.manager.connected.test {
            env.manager.acquire("ui")
            runCurrent()
            assertEquals(0, env.refreshes)
            env.connector.attempts.last().open()
            runCurrent()
            awaitItem()
            assertEquals(1, env.refreshes)
            env.connector.attempts.last().fail()
            advanceTimeBy(1_000)
            runCurrent()
            env.connector.attempts.last().open()
            runCurrent()
            awaitItem()
            assertEquals(2, env.refreshes)
        }
    }

    @Test
    fun reconnectInvalidatesSnapshotBeforeReportingConnected() = runTest {
        val env = env()
        val freshnessWhenConnected = mutableListOf<Boolean>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            env.manager.state.collect { connection ->
                if (connection == ConnectionState.Connected) freshnessWhenConnected += env.store.snapshotFresh.value
            }
        }
        env.manager.acquire("ui")
        runCurrent()
        env.connector.attempts.last().open()
        runCurrent()
        env.connector.attempts.last().send(Fixtures.text("ws-snapshot.json"))
        runCurrent()
        assertTrue(env.store.snapshotFresh.value)

        env.connector.attempts.last().fail()
        runCurrent()
        advanceTimeBy(1_000)
        runCurrent()
        env.connector.attempts.last().open()
        runCurrent()

        assertEquals(listOf(false, false), freshnessWhenConnected)
        assertFalse(env.store.snapshotFresh.value)
        env.connector.attempts.last().send(Fixtures.text("ws-snapshot.json"))
        runCurrent()
        assertTrue(env.store.snapshotFresh.value)
    }

    @Test
    fun messagesFeedTheStoreAndGarbageIsIgnored() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        val socket = env.connector.attempts.last()
        socket.open()
        socket.send("not json")
        socket.send("""{"type":"from_the_future","x":1}""")
        socket.send("""{"type":"job_added"}""")
        socket.send(Fixtures.text("ws-snapshot.json"))
        runCurrent()
        assertEquals(4, env.store.jobs.value.size)
        assertTrue(env.store.hasSnapshot.value)
        assertEquals(ConnectionState.Connected, env.manager.state.value)
        socket.send(Fixtures.text("ws-job_removed.json"))
        socket.send(Fixtures.text("ws-job_updated.json"))
        runCurrent()
        assertEquals(4, env.store.jobs.value.size)
    }

    @Test
    fun configChangeReconnectsImmediatelyWithFreshBackoff() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        // Push the backoff up first.
        repeat(3) {
            env.connector.attempts.last().fail()
            runCurrent()
            advanceTimeBy(30_000)
            runCurrent()
        }
        val first = env.connector.attempts.last()
        val before = env.connector.attempts.size
        val t = currentTime
        env.config.config.value = b
        runCurrent()
        assertTrue(first.closed)
        assertEquals(before + 1, env.connector.attempts.size)
        assertEquals(b, env.connector.attempts.last().config)
        assertEquals(t, env.connector.attempts.last().time)
        env.connector.attempts.last().fail()
        runCurrent()
        assertEquals(ConnectionState.Disconnected(1), env.manager.state.value)

        // Token-only change also reconnects.
        env.config.config.value = b.copy(token = "other")
        runCurrent()
        assertEquals(before + 2, env.connector.attempts.size)
    }

    @Test
    fun urlChangeClearsStoreTokenChangeKeepsIt() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        env.connector.attempts.last().open()
        env.connector.attempts.last().send(Fixtures.text("ws-snapshot.json"))
        runCurrent()
        env.config.config.value = a.copy(token = "new")
        runCurrent()
        assertEquals(4, env.store.jobs.value.size)
        env.config.config.value = b
        runCurrent()
        assertTrue(env.store.jobs.value.isEmpty())
        assertFalse(env.store.hasSnapshot.value)
    }

    @Test
    fun configRemovedDisconnectsAndClears() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        val socket = env.connector.attempts.last()
        socket.open()
        socket.send(Fixtures.text("ws-snapshot.json"))
        runCurrent()
        env.config.config.value = null
        runCurrent()
        assertTrue(socket.closed)
        assertEquals(ConnectionState.NotConfigured, env.manager.state.value)
        assertTrue(env.store.jobs.value.isEmpty())
        advanceTimeBy(60_000)
        runCurrent()
        assertEquals(1, env.connector.attempts.size)
    }

    @Test
    fun releasingTheLastHolderClosesAndStopsRetrying() = runTest {
        val env = env()
        env.manager.acquire("ui")
        env.manager.acquire("service")
        runCurrent()
        val socket = env.connector.attempts.last()
        socket.open()
        runCurrent()
        env.manager.release("ui")
        runCurrent()
        assertFalse(socket.closed)
        assertEquals(ConnectionState.Connected, env.manager.state.value)
        env.manager.release("service")
        runCurrent()
        assertTrue(socket.closed)
        assertEquals(ConnectionState.Disconnected(null), env.manager.state.value)
        advanceTimeBy(120_000)
        runCurrent()
        assertEquals(1, env.connector.attempts.size)
    }

    @Test
    fun releasingWhileWaitingToRetryCancelsTheRetry() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        env.connector.attempts.last().fail()
        runCurrent()
        env.manager.release("ui")
        runCurrent()
        advanceTimeBy(60_000)
        runCurrent()
        assertEquals(1, env.connector.attempts.size)
        assertEquals(ConnectionState.Disconnected(null), env.manager.state.value)
        env.manager.acquire("ui")
        runCurrent()
        assertEquals(2, env.connector.attempts.size)
    }

    @Test
    fun extraReleaseIsHarmless() = runTest {
        val env = env()
        env.manager.release("ui")
        env.manager.acquire("ui")
        runCurrent()
        assertEquals(1, env.connector.attempts.size)
    }

    @Test
    fun reconnectNowClosesAndReconnectsWithFreshBackoff() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        repeat(3) {
            env.connector.attempts.last().fail()
            runCurrent()
            advanceTimeBy(30_000)
            runCurrent()
        }
        val old = env.connector.attempts.last()
        val before = env.connector.attempts.size
        env.manager.reconnectNow()
        runCurrent()
        assertTrue(old.closed)
        assertEquals(before + 1, env.connector.attempts.size)
        env.connector.attempts.last().fail()
        runCurrent()
        assertEquals(ConnectionState.Disconnected(1), env.manager.state.value)
    }

    @Test
    fun staleSocketEventsAreIgnoredAfterReconnect() = runTest {
        val env = env()
        env.manager.acquire("ui")
        runCurrent()
        val old = env.connector.attempts.last()
        env.manager.reconnectNow()
        runCurrent()
        old.open()
        old.send(Fixtures.text("ws-snapshot.json"))
        old.fail()
        runCurrent()
        assertEquals(ConnectionState.Connecting, env.manager.state.value)
        assertTrue(env.store.jobs.value.isEmpty())
        assertEquals(2, env.connector.attempts.size)
    }
}
