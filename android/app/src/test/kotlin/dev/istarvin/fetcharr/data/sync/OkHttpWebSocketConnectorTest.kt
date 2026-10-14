package dev.istarvin.fetcharr.data.sync

import dev.istarvin.fetcharr.data.network.ServerConfig
import okhttp3.OkHttpClient
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

class OkHttpWebSocketConnectorTest {
    private lateinit var server: MockWebServer
    private val connector = OkHttpWebSocketConnector(OkHttpClient())
    private val events = LinkedBlockingQueue<String>()
    private val serverEvents = LinkedBlockingQueue<String>()

    private val clientListener = object : WebSocketConnector.Listener {
        override fun onOpen() {
            events.add("open")
        }

        override fun onMessage(text: String) {
            events.add("message:$text")
        }

        override fun onClosed() {
            events.add("closed")
        }

        override fun onFailure(error: Throwable) {
            events.add("failure")
        }
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun config(token: String?) = ServerConfig(server.url("/").toString().trimEnd('/'), token)

    private fun next(queue: LinkedBlockingQueue<String>): String =
        checkNotNull(queue.poll(5, TimeUnit.SECONDS)) { "timed out waiting for an event" }

    private fun upgrade(onOpen: (WebSocket) -> Unit = {}) {
        server.enqueue(
            MockResponse().withWebSocketUpgrade(
                object : WebSocketListener() {
                    override fun onOpen(webSocket: WebSocket, response: Response) = onOpen(webSocket)

                    override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                        serverEvents.add("closing:$code")
                        webSocket.close(code, null)
                    }
                },
            ),
        )
    }

    @Test
    fun sendsBearerTokenToApiWsAndDeliversFrames() {
        upgrade { it.send("""{"type":"snapshot","jobs":[]}"""); it.send("second") }
        val handle = connector.connect(config("secret"), clientListener)
        assertEquals("open", next(events))
        assertEquals("""message:{"type":"snapshot","jobs":[]}""", next(events))
        assertEquals("message:second", next(events))

        val request = server.takeRequest(5, TimeUnit.SECONDS)!!
        assertEquals("/api/ws", request.path)
        assertEquals("Bearer secret", request.getHeader("Authorization"))
        assertEquals("websocket", request.getHeader("Upgrade"))
        handle.close()
    }

    @Test
    fun noAuthorizationHeaderWithoutToken() {
        upgrade()
        val handle = connector.connect(config("  "), clientListener)
        assertEquals("open", next(events))
        assertNull(server.takeRequest(5, TimeUnit.SECONDS)!!.getHeader("Authorization"))
        handle.close()
    }

    @Test
    fun closeSendsNormalClosureToTheServer() {
        upgrade()
        val handle = connector.connect(config(null), clientListener)
        assertEquals("open", next(events))
        handle.close()
        assertEquals("closing:1000", next(serverEvents))
    }

    @Test
    fun serverCloseIsReported() {
        upgrade { it.close(1000, "bye") }
        connector.connect(config(null), clientListener)
        assertEquals("open", next(events))
        assertEquals("closed", next(events))
    }

    @Test
    fun rejectedUpgradeIsAFailure() {
        server.enqueue(MockResponse().setResponseCode(401))
        connector.connect(config("bad"), clientListener)
        assertEquals("failure", next(events))
    }

    @Test
    fun unreachableServerIsAFailure() {
        val cfg = config(null)
        server.shutdown()
        connector.connect(cfg, clientListener)
        assertEquals("failure", next(events))
    }

    @Test
    fun invalidUrlIsAFailureNotACrash() {
        val handle = connector.connect(ServerConfig("not a url", null), clientListener)
        assertEquals("failure", next(events))
        handle.close()
        assertTrue(events.isEmpty())
    }
}
