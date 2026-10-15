package com.istarvin.fetcharr.data.network

import kotlinx.serialization.json.Json

/** The one Json configuration used for the wire format. Also provided by `NetworkModule`. */
val FetcharrJson: Json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
}
