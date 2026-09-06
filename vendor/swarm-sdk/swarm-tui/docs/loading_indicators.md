# Loading Indicators

SwarmOS now includes animated loading indicators to provide clear visual feedback when the agent is working.

## Features

### Visual Feedback Types
- **Spinner** (⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏): Classic rotating braille pattern
- **Dots** (...): Animated ellipsis 
- **Glitch** (█▓▒░): Matrix-style cyberpunk effect
- **Pulse** (●◐○): Pulsing circle with sine wave
- **Bar** (████░░): Moving progress bar

### Smart Integration
- Automatically starts when agent begins processing
- Shows elapsed time after 1 second
- Updates conversation status indicators
- Stops when agent completes or encounters errors

### User Controls
- `Ctrl+L`: Cycle through indicator types
- `Ctrl+Shift+T`: Test mode (manual toggle for demos)

### Technical Details
- Updates every 100ms for smooth animation
- Themed colors using app accent color
- Positioned at bottom of message viewport  
- Integrates with existing status system

## Code Structure

```
internal/chat/loading_indicators.go - Core indicator types and animations
internal/chat/app.go - Integration with app state and UI
```

## Usage

The loading indicators work automatically - just send a message and watch the visual feedback. Use `Ctrl+L` to try different animation styles!