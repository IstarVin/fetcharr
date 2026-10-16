package com.istarvin.fetcharr.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

private val Base = Typography()

/** The stock Material scale with firmer weights for titles and a bit more air in body copy. */
internal val AppTypography = Typography(
    headlineMedium = Base.headlineMedium.copy(fontWeight = FontWeight.SemiBold),
    headlineSmall = Base.headlineSmall.copy(fontWeight = FontWeight.SemiBold),
    titleLarge = Base.titleLarge.copy(fontWeight = FontWeight.SemiBold),
    titleMedium = Base.titleMedium.copy(fontWeight = FontWeight.SemiBold, letterSpacing = 0.1.sp),
    titleSmall = Base.titleSmall.copy(fontWeight = FontWeight.SemiBold),
    bodyMedium = Base.bodyMedium.copy(lineHeight = 21.sp),
    labelLarge = Base.labelLarge.copy(fontWeight = FontWeight.SemiBold, fontFeatureSettings = "tnum"),
    labelMedium = Base.labelMedium.copy(fontWeight = FontWeight.SemiBold, fontFeatureSettings = "tnum"),
    labelSmall = Base.labelSmall.copy(fontWeight = FontWeight.Medium),
    // Tabular figures: speeds, sizes and percentages tick without the line jittering sideways.
    bodySmall = Base.bodySmall.copy(fontFeatureSettings = "tnum"),
)

/** Monospaced style for URLs, paths and numbers that should line up. */
val MonoStyle = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 12.sp, lineHeight = 16.sp)
